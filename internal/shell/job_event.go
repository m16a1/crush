package shell

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/pubsub"
)

// Job event types. Type distinguishes a mid-flight output chunk from the
// single terminal event emitted when the job exits.
const (
	JobEventOutput = "output"
	JobEventDone   = "done"
)

// JobEvent reports the lifecycle of a background job to interested
// observers. Output events carry a coalesced chunk of stdout/stderr; the
// terminal event carries the exit code and an empty chunk.
//
// JobEvents are a process-local notification stream, not a durable record:
// they exist so the UI can render a running job's output live and so the
// session can be told when a job finishes. The shell manager remains the
// source of truth for the output itself.
type JobEvent struct {
	Type        string `json:"type"`
	ShellID     string `json:"shell_id"`
	SessionID   string `json:"session_id,omitempty"`
	PID         int    `json:"pid,omitempty"`
	Command     string `json:"command,omitempty"`
	Description string `json:"description,omitempty"`
	Chunk       string `json:"chunk,omitempty"`
	ExitCode    int    `json:"exit_code,omitempty"`
}

var jobBroker = pubsub.NewBroker[JobEvent]()

// jobOutputFlushInterval bounds how long buffered job output can sit before
// it is forwarded as a live chunk. Newline-terminated writes flush sooner.
const jobOutputFlushInterval = 100 * time.Millisecond

// SubscribeJobEvents returns a channel of background-job events for the
// lifetime of ctx.
func SubscribeJobEvents(ctx context.Context) <-chan pubsub.Event[JobEvent] {
	return jobBroker.Subscribe(ctx)
}

// publishJobEvent is best-effort: dropping a mid-flight chunk under
// back-pressure is acceptable because the full output is always available
// from the manager afterwards. The terminal event is published the same way
// for symmetry; the wake path reads the output from the job itself.
func publishJobEvent(ev JobEvent) {
	jobBroker.Publish(pubsub.UpdatedEvent, ev)
}

// jobOutputCoalescer batches output writes into JobEvent chunks. Writes are
// forwarded when they contain a newline or when flushInterval elapses,
// whichever comes first, so line-oriented output arrives promptly and a
// noisy burst does not produce one broker event per write syscall.
type jobOutputCoalescer struct {
	chunks        chan string
	flushInterval time.Duration
	flush         func(string)
	mu            sync.Mutex
	buf           strings.Builder
	done          chan struct{}
	stopped       chan struct{}
	closeOnce     sync.Once
}

func newJobOutputCoalescer(flushInterval time.Duration, flush func(string)) *jobOutputCoalescer {
	return &jobOutputCoalescer{
		chunks:        make(chan string, 256),
		flushInterval: flushInterval,
		flush:         flush,
		done:          make(chan struct{}),
		stopped:       make(chan struct{}),
	}
}

func (c *jobOutputCoalescer) run() {
	defer close(c.stopped)
	ticker := time.NewTicker(c.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case chunk := <-c.chunks:
			c.append(chunk)
			if strings.Contains(chunk, "\n") {
				c.emit()
			}
		case <-ticker.C:
			c.emit()
		case <-c.done:
			c.drain()
			c.emit()
			return
		}
	}
}

// write enqueues a chunk without blocking. Under extreme back-pressure the
// chunk is dropped; the full output stays readable via the manager.
func (c *jobOutputCoalescer) write(chunk string) {
	select {
	case c.chunks <- chunk:
	default:
	}
}

// close flushes any buffered output and waits for the coalescer goroutine to
// stop, so no output event can be published after close returns. It is safe
// to call more than once.
func (c *jobOutputCoalescer) close() {
	c.closeOnce.Do(func() { close(c.done) })
	<-c.stopped
}

func (c *jobOutputCoalescer) append(chunk string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.WriteString(chunk)
}

func (c *jobOutputCoalescer) drain() {
	for {
		select {
		case chunk := <-c.chunks:
			c.append(chunk)
		default:
			return
		}
	}
}

func (c *jobOutputCoalescer) emit() {
	c.mu.Lock()
	out := c.buf.String()
	c.buf.Reset()
	c.mu.Unlock()
	if out != "" {
		c.flush(out)
	}
}
