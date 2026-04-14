package stream

import (
	"sync"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

const bufferSize = 64

// Manager implements port.StreamManager using a sync.Map of per-job subscriber lists.
type Manager struct {
	mu   sync.Mutex
	subs map[string][]chan port.StreamEvent
}

var _ port.StreamManager = (*Manager)(nil)

func New() *Manager {
	return &Manager{subs: make(map[string][]chan port.StreamEvent)}
}

func (m *Manager) Publish(jobID string, event port.StreamEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.subs[jobID] {
		select {
		case ch <- event:
		default:
			// drop if subscriber is slow
		}
	}
}

func (m *Manager) Subscribe(jobID string) <-chan port.StreamEvent {
	ch := make(chan port.StreamEvent, bufferSize)
	m.mu.Lock()
	m.subs[jobID] = append(m.subs[jobID], ch)
	m.mu.Unlock()
	return ch
}

func (m *Manager) Unsubscribe(jobID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.subs, jobID)
}
