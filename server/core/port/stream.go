package port

// SSE event type names published by WorkerService and consumed by REST handlers.
const (
	EventToken  = "token"
	EventDone   = "done"
	EventError  = "error"
	EventStatus = "status"
)

// SSE event payload field keys used in the JSON data body of each event.
const (
	EventFieldText  = "text"
	EventFieldError = "error"
)

// StreamManager manages per-job SSE event channels.
// Implemented by store/stream.
type StreamManager interface {
	// Publish sends an event to all subscribers of the given job.
	Publish(jobID string, event StreamEvent)
	// Subscribe returns a channel that receives events for the given job.
	// The caller must call Unsubscribe when done.
	Subscribe(jobID string) <-chan StreamEvent
	// Unsubscribe removes the subscription for the given job.
	Unsubscribe(jobID string)
}

// StreamEvent is a single SSE event sent to a subscribed client.
type StreamEvent struct {
	Type string
	Data string
}
