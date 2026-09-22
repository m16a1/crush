// Package httperror rewrites provider error responses so the provider's own
// message reaches the caller instead of the raw response body.
//
// OpenAI and Anthropic compatible servers do not always answer a failed request
// with the error envelope the SDKs expect. A common shape is a bare string:
//
//	{"error": "invalid api key"}
//
// The SDKs report the body it cannot read into their own error type verbatim,
// so callers see "unauthorized: {\"error\":\"invalid api key\"}": the provider's
// wording is buried in JSON, and an empty body or an HTML page from a gateway
// is shown as-is.
//
// Rewriting such bodies into the expected envelope leaves the error's status
// and classification untouched and turns the message into the provider's own
// text: "unauthorized: invalid api key".
package httperror

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	// maxErrorBody is the most of an error response that is read and rewritten.
	// Error bodies are small; anything larger is usually an HTML page served by
	// a proxy, which is truncated into the message instead.
	maxErrorBody = 64 << 10

	// maxMessageLength caps the message kept from a body that is not JSON.
	maxMessageLength = 2 << 10

	// truncatedSuffix marks a message that was cut short.
	truncatedSuffix = "…"
)

// messageKeys are the fields non-OpenAI compatible servers use for the
// human-readable error text.
var messageKeys = []string{"message", "detail", "error_description", "msg", "reason"}

// Transport wraps base so that error responses which do not match the provider
// SDKs' error envelope are rewritten before the SDK reads them. Successful
// responses are passed through untouched, so streaming is unaffected.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &normalizingTransport{base: base}
}

// WithNormalizedErrors returns client with error body normalization installed,
// allocating a client when given nil.
func WithNormalizedErrors(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	client.Transport = Transport(client.Transport)
	return client
}

type normalizingTransport struct {
	base http.RoundTripper
}

func (t *normalizingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	if resp.StatusCode < http.StatusBadRequest || resp.Body == nil {
		return resp, nil
	}
	if resp.Header.Get("Content-Encoding") != "" && !resp.Uncompressed {
		// The body is still compressed, so rewriting it would corrupt it.
		return resp, nil
	}

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	_ = resp.Body.Close()

	body, changed := NormalizeBody(raw, resp.StatusCode)
	switch {
	case changed:
		// Use the rewritten body.
	case readErr != nil:
		body = errorEnvelope("could not read the provider's error response: " + readErr.Error())
	default:
		body = raw
	}

	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return resp, nil
}

// NormalizeBody rewrites body, the payload of an error response, into the
// envelope the provider SDKs decode:
//
//	{"error": {"message": "..."}}
//
// It reports whether the body changed, returning it untouched when it already
// has the expected shape so that well-formed provider errors keep their fields.
func NormalizeBody(body []byte, status int) ([]byte, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return errorEnvelope(emptyBodyMessage(status)), true
	}

	if trimmed[0] == '{' {
		fields := map[string]json.RawMessage{}
		if err := json.Unmarshal(trimmed, &fields); err == nil {
			if raw, ok := fields["error"]; ok && isJSONObject(raw) {
				// Already the envelope the SDK expects.
				return body, false
			}
			detail, err := marshalJSON(errorDetail{Message: objectMessage(fields, trimmed)})
			if err != nil {
				return body, false
			}
			fields["error"] = detail
			normalized, err := marshalJSON(fields)
			if err != nil {
				return body, false
			}
			return normalized, true
		}
	}

	// A bare JSON string or number, an array, or not JSON at all (an HTML page
	// served by a gateway, for example).
	return errorEnvelope(textMessage(trimmed)), true
}

// errorDetail is the part of the error envelope the SDKs read. The other fields
// (code, param, type) are optional, and provider errors that carry them already
// have the expected shape and are passed through untouched.
type errorDetail struct {
	Message string `json:"message"`
}

type errorEnvelopeBody struct {
	Error errorDetail `json:"error"`
}

// errorEnvelope builds the error envelope. Marshalling a fixed struct of
// strings cannot fail.
func errorEnvelope(message string) []byte {
	body, _ := marshalJSON(errorEnvelopeBody{Error: errorDetail{Message: message}})
	return body
}

// marshalJSON marshals v without escaping HTML, so a message carrying markup
// from a gateway stays readable in logs and error dumps.
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// isJSONObject reports whether raw holds a JSON object.
func isJSONObject(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// objectMessage returns the error text of an error object: the value of its
// error field when that carries text, then any other field servers commonly use
// for the message, and the whole object as a last resort.
func objectMessage(fields map[string]json.RawMessage, body []byte) string {
	if raw, ok := fields["error"]; ok {
		if text := rawText(raw, 0); text != "" {
			return text
		}
	}
	for _, key := range messageKeys {
		if raw, ok := fields[key]; ok {
			if text := rawText(raw, 0); text != "" {
				return text
			}
		}
	}
	return textMessage(body)
}

// maxMessageDepth bounds how deep rawText follows nested objects, so a
// pathologically nested body cannot exhaust the stack.
const maxMessageDepth = 5

// rawText returns the readable text of a JSON value: the string itself, or the
// message of an object that wraps one. It returns an empty string for values
// that carry no text.
func rawText(raw []byte, depth int) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return ""
		}
		return truncateMessage(trimmed)
	}
	if depth >= maxMessageDepth {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ""
	}
	for _, key := range append([]string{"error"}, messageKeys...) {
		if nested, ok := fields[key]; ok {
			if text := rawText(nested, depth+1); text != "" {
				return text
			}
		}
	}
	return ""
}

// textMessage returns the error text of a body that is not an error object:
// the string itself when it is a JSON string, and the raw text otherwise.
func textMessage(body []byte) string {
	var text string
	if err := json.Unmarshal(body, &text); err == nil && strings.TrimSpace(text) != "" {
		return truncateMessage(text)
	}
	return truncateMessage(strings.Join(strings.Fields(string(body)), " "))
}

// emptyBodyMessage describes an error response that carried no body at all.
func emptyBodyMessage(status int) string {
	if text := http.StatusText(status); text != "" {
		return fmt.Sprintf("the provider returned HTTP %d (%s) with an empty response body", status, text)
	}
	return fmt.Sprintf("the provider returned HTTP %d with an empty response body", status)
}

// truncateMessage cuts a message down to maxMessageLength runes, so a whole
// HTML page does not end up in the UI.
func truncateMessage(message string) string {
	if len(message) <= maxMessageLength {
		return message
	}
	runes := 0
	for i := range message {
		if runes == maxMessageLength {
			return message[:i] + truncatedSuffix
		}
		runes++
	}
	return message
}
