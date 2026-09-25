package question

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

func validQuestion() Question {
	return Question{
		ID:          "q1",
		Type:        TypeYesNo,
		Label:       "deploy",
		Text:        "Deploy to production?",
		Description: "Ships the current build to all regions.",
	}
}

func validRequest() Request {
	return Request{
		SessionID:  "sess-1",
		ToolCallID: "call-1",
		Questions:  []Question{validQuestion()},
	}
}

func TestQuestionValidateAcceptsEachSupportedShape(t *testing.T) {
	t.Parallel()

	tests := map[string]Question{
		"yes_no":    {Type: TypeYesNo, Text: "ok?", Description: "d"},
		"free_text": {Type: TypeFreeText, Text: "name?", Description: "d"},
		"single_choice": {
			Type: TypeSingleChoice, Text: "pick?", Description: "d",
			Choices: []Choice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
		},
		"multi_choice": {
			Type: TypeMultiChoice, Text: "pick many?", Description: "d",
			Choices: []Choice{{ID: "a", Label: "A", Description: "first"}, {ID: "b", Label: "B"}},
		},
	}

	for name, q := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, q.Validate())
		})
	}
}

func TestQuestionValidateRejectsBadInput(t *testing.T) {
	t.Parallel()

	choices := func(n int) []Choice {
		out := make([]Choice, n)
		for i := range out {
			out[i] = Choice{ID: string(rune('a' + i)), Label: "label"}
		}
		return out
	}

	tests := map[string]struct {
		mutate    func(*Question)
		errSubstr string
	}{
		"missing text": {
			mutate:    func(q *Question) { q.Text = "" },
			errSubstr: "question text is required",
		},
		"text too long": {
			mutate:    func(q *Question) { q.Text = strings.Repeat("x", MaxQuestionLength+1) },
			errSubstr: "text exceeds 240 characters",
		},
		"missing description": {
			mutate:    func(q *Question) { q.Description = "" },
			errSubstr: "description is required",
		},
		"description too long": {
			mutate:    func(q *Question) { q.Description = strings.Repeat("x", MaxDescriptionLength+1) },
			errSubstr: "description exceeds 600 characters",
		},
		"unknown type": {
			mutate:    func(q *Question) { q.Type = "ranking" },
			errSubstr: `unknown type "ranking"`,
		},
		"single choice needs two options": {
			mutate: func(q *Question) {
				q.Type = TypeSingleChoice
				q.Choices = choices(1)
			},
			errSubstr: "requires at least 2 choices",
		},
		"multi choice needs two options": {
			mutate: func(q *Question) {
				q.Type = TypeMultiChoice
				q.Choices = choices(1)
			},
			errSubstr: "requires at least 2 choices",
		},
		"too many choices": {
			mutate: func(q *Question) {
				q.Type = TypeSingleChoice
				q.Choices = choices(MaxChoices + 1)
			},
			errSubstr: "choices exceed maximum of 5",
		},
		"choice without id": {
			mutate: func(q *Question) {
				q.Type = TypeSingleChoice
				q.Choices = []Choice{{Label: "A"}, {ID: "b", Label: "B"}}
			},
			errSubstr: `choice 1 must have an "id" field`,
		},
		"duplicate choice id": {
			mutate: func(q *Question) {
				q.Type = TypeSingleChoice
				q.Choices = []Choice{{ID: "a", Label: "A"}, {ID: "a", Label: "B"}}
			},
			errSubstr: `duplicate id "a"`,
		},
		"choice without label": {
			mutate: func(q *Question) {
				q.Type = TypeSingleChoice
				q.Choices = []Choice{{ID: "a"}, {ID: "b", Label: "B"}}
			},
			errSubstr: `choice 1 (a) must have a "label" field`,
		},
		"choice label too long": {
			mutate: func(q *Question) {
				q.Type = TypeSingleChoice
				q.Choices = []Choice{
					{ID: "a", Label: strings.Repeat("x", MaxChoiceLabelLength+1)},
					{ID: "b", Label: "B"},
				}
			},
			errSubstr: "label exceeds 200 characters",
		},
		"choice description too long": {
			mutate: func(q *Question) {
				q.Type = TypeSingleChoice
				q.Choices = []Choice{
					{ID: "a", Label: "A", Description: strings.Repeat("x", MaxChoiceDescriptionLength+1)},
					{ID: "b", Label: "B"},
				}
			},
			errSubstr: "description exceeds 200 characters",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			q := validQuestion()
			tc.mutate(&q)
			err := q.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.errSubstr)
		})
	}
}

func TestQuestionValidatePrefixesErrorsWithTheLabel(t *testing.T) {
	t.Parallel()

	q := validQuestion()
	q.Text = ""
	require.Contains(t, q.Validate().Error(), "[deploy]: question text is required")

	q = validQuestion()
	q.Label = ""
	q.Text = strings.Repeat("z", 60)
	q.Description = ""
	require.Contains(t, q.Validate().Error(), "["+strings.Repeat("z", 40)+"…]")

	require.Equal(t, "[unnamed question]", Question{}.identifier())
}

func TestRequestValidate(t *testing.T) {
	t.Parallel()

	require.NoError(t, validRequest().Validate())

	err := Request{}.Validate()
	require.ErrorContains(t, err, "at least one question is required")

	req := validRequest()
	for range MaxQuestions {
		req.Questions = append(req.Questions, validQuestion())
	}
	require.ErrorContains(t, req.Validate(), "questions exceed maximum of 5")

	req = validRequest()
	req.Questions = append(req.Questions, validQuestion(), func() Question {
		q := validQuestion()
		q.Text = ""
		return q
	}())
	require.ErrorContains(t, req.Validate(), "question 3:")
}

func TestAnswerHasNotes(t *testing.T) {
	t.Parallel()

	require.False(t, Answer{}.HasNotes())
	require.True(t, Answer{Notes: map[string]string{"a": "b"}}.HasNotes())
}

func answerAndAwait(t *testing.T, svc *questionService, req Request, respond func() bool) []Answer {
	t.Helper()

	sub := svc.Subscribe(t.Context())
	results := make(chan []Answer, 1)
	errs := make(chan error, 1)
	go func() {
		answers, err := svc.Ask(t.Context(), req)
		results <- answers
		errs <- err
	}()

	select {
	case <-sub:
	case <-time.After(2 * time.Second):
		t.Fatal("Ask never published its request")
	}

	require.True(t, respond())

	select {
	case answers := <-results:
		require.NoError(t, <-errs)
		return answers
	case <-time.After(2 * time.Second):
		t.Fatal("Ask did not return after being answered")
		return nil
	}
}

func TestAskReturnsTheAnswersAndNotifies(t *testing.T) {
	t.Parallel()

	svc := NewService()
	notes := svc.SubscribeNotifications(t.Context())

	want := []Answer{{QuestionID: "q1", Yes: boolPtr(true), Notes: map[string]string{"why": "because"}}}
	got := answerAndAwait(t, svc, validRequest(), func() bool { return svc.Answer(want) })

	require.Equal(t, want, got)

	select {
	case ev := <-notes:
		require.Equal(t, pubsub.CreatedEvent, ev.Type)
		require.NotEmpty(t, ev.Payload.BatchID, "the notification must carry the resolved batch id")
	case <-time.After(2 * time.Second):
		t.Fatal("Answer did not publish a notification")
	}
}

func TestAskFillsInRequestAndQuestionIDs(t *testing.T) {
	t.Parallel()

	svc := NewService()
	sub := svc.Subscribe(t.Context())

	req := validRequest()
	req.ID = ""
	req.Questions[0].ID = ""

	go func() {
		_, _ = svc.Ask(t.Context(), req)
	}()

	select {
	case ev := <-sub:
		require.NotEmpty(t, ev.Payload.ID)
		require.NotEmpty(t, ev.Payload.Questions[0].ID)
	case <-time.After(2 * time.Second):
		t.Fatal("Ask never published its request")
	}

	svc.Answer(nil)
}

func TestAskDefaultsConfirmFieldsForMultipleQuestions(t *testing.T) {
	t.Parallel()

	svc := NewService()
	sub := svc.Subscribe(t.Context())

	req := validRequest()
	req.Questions = append(req.Questions, validQuestion())

	go func() {
		_, _ = svc.Ask(t.Context(), req)
	}()

	select {
	case ev := <-sub:
		require.Equal(t, "Ready to go?", ev.Payload.ConfirmTitle)
		require.Equal(t, "Review your answers above and confirm.", ev.Payload.ConfirmDescription)
	case <-time.After(2 * time.Second):
		t.Fatal("Ask never published its request")
	}

	svc.Answer(nil)
}

func TestAskKeepsExplicitConfirmFields(t *testing.T) {
	t.Parallel()

	svc := NewService()
	sub := svc.Subscribe(t.Context())

	req := validRequest()
	req.Questions = append(req.Questions, validQuestion())
	req.ConfirmTitle = "Custom"
	req.ConfirmDescription = "Custom body"

	go func() {
		_, _ = svc.Ask(t.Context(), req)
	}()

	select {
	case ev := <-sub:
		require.Equal(t, "Custom", ev.Payload.ConfirmTitle)
		require.Equal(t, "Custom body", ev.Payload.ConfirmDescription)
	case <-time.After(2 * time.Second):
		t.Fatal("Ask never published its request")
	}

	svc.Answer(nil)
}

func TestAskRejectsAnInvalidRequestWithoutPublishing(t *testing.T) {
	t.Parallel()

	svc := NewService()
	sub := svc.Subscribe(t.Context())

	_, err := svc.Ask(t.Context(), Request{})
	require.ErrorContains(t, err, "at least one question is required")

	select {
	case ev := <-sub:
		t.Fatalf("invalid request was published: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAskReturnsErrCancelledWhenCancelled(t *testing.T) {
	t.Parallel()

	svc := NewService()
	notes := svc.SubscribeNotifications(t.Context())

	sub := svc.Subscribe(t.Context())
	errs := make(chan error, 1)
	go func() {
		_, err := svc.Ask(t.Context(), validRequest())
		errs <- err
	}()

	select {
	case <-sub:
	case <-time.After(2 * time.Second):
		t.Fatal("Ask never published its request")
	}

	require.True(t, svc.Cancel())

	select {
	case err := <-errs:
		require.ErrorIs(t, err, ErrCancelled)
	case <-time.After(2 * time.Second):
		t.Fatal("Ask did not return after Cancel")
	}

	select {
	case ev := <-notes:
		require.NotEmpty(t, ev.Payload.BatchID)
	case <-time.After(2 * time.Second):
		t.Fatal("Cancel did not publish a notification")
	}
}

func TestAskHonoursContextCancellation(t *testing.T) {
	t.Parallel()

	svc := NewService()
	sub := svc.Subscribe(t.Context())
	ctx, cancel := context.WithCancel(t.Context())

	errs := make(chan error, 1)
	go func() {
		_, err := svc.Ask(ctx, validRequest())
		errs <- err
	}()

	select {
	case <-sub:
	case <-time.After(2 * time.Second):
		t.Fatal("Ask never published its request")
	}

	cancel()

	select {
	case err := <-errs:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Ask did not return after the context was cancelled")
	}
}

func TestAnswerAndCancelWithoutAPendingQuestionReturnFalse(t *testing.T) {
	t.Parallel()

	svc := NewService()
	require.False(t, svc.Answer(nil))
	require.False(t, svc.Cancel())
}

func boolPtr(b bool) *bool { return &b }
