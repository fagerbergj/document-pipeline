package stream_test

import (
	"testing"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/fagerbergj/document-pipeline/server/store/stream"
)

func TestPublishSubscribe(t *testing.T) {
	m := stream.New()
	ch := m.Subscribe("job-1")

	evt := port.StreamEvent{Type: "token", Data: "hello"}
	m.Publish("job-1", evt)

	select {
	case got := <-ch:
		if got != evt {
			t.Fatalf("got %+v, want %+v", got, evt)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestUnsubscribe_ClearsJob(t *testing.T) {
	m := stream.New()
	_ = m.Subscribe("job-2")
	m.Unsubscribe("job-2")
	// Publish after unsubscribe should not block or panic.
	m.Publish("job-2", port.StreamEvent{Type: "done"})
}

func TestPublish_NoSubscribers(t *testing.T) {
	m := stream.New()
	// Should not panic.
	m.Publish("job-x", port.StreamEvent{Type: "token", Data: "x"})
}

func TestMultipleSubscribers(t *testing.T) {
	m := stream.New()
	ch1 := m.Subscribe("job-3")
	ch2 := m.Subscribe("job-3")

	evt := port.StreamEvent{Type: "token", Data: "broadcast"}
	m.Publish("job-3", evt)

	for _, ch := range []<-chan port.StreamEvent{ch1, ch2} {
		select {
		case got := <-ch:
			if got != evt {
				t.Fatalf("got %+v, want %+v", got, evt)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for event on subscriber")
		}
	}
}
