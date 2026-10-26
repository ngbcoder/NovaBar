package notifications

import (
	"fmt"
	"sync"
	"time"
)

type Notification struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	AppName   string    `json:"app_name"`
	Timestamp time.Time `json:"timestamp"`
	Read      bool      `json:"read"`
}

type Manager struct {
	mu            sync.Mutex
	notifications []Notification
	dnd           bool
	nextID        int
}

func NewManager() *Manager {
	return &Manager{
		notifications: make([]Notification, 0, 32),
	}
}

func (m *Manager) Add(title, body, appName string) Notification {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	n := Notification{
		ID:        fmt.Sprintf("notif-%d", m.nextID),
		Title:     title,
		Body:      body,
		AppName:   appName,
		Timestamp: time.Now(),
		Read:      false,
	}
	m.notifications = append([]Notification{n}, m.notifications...)
	if len(m.notifications) > 100 {
		m.notifications = m.notifications[:100]
	}
	return n
}

func (m *Manager) List() []Notification {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Notification, len(m.notifications))
	copy(out, m.notifications)
	return out
}

func (m *Manager) Dismiss(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, n := range m.notifications {
		if n.ID == id {
			m.notifications = append(m.notifications[:i], m.notifications[i+1:]...)
			return true
		}
	}
	return false
}

func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifications = m.notifications[:0]
}

func (m *Manager) SetDND(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dnd = enabled
}

func (m *Manager) DND() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dnd
}
