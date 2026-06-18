package web

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"
)

type TaskStatus string

const (
	TaskQueued  TaskStatus = "queued"
	TaskRunning TaskStatus = "running"
	TaskDone    TaskStatus = "done"
	TaskError   TaskStatus = "error"
)

// TaskFile is the display-only summary of one uploaded top-level path: its base
// name and size. Size>=0 is bytes; Size==-1 means a directory (batch upload, no
// single size); Size==-2 means stat failed / unknown.
type TaskFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Task is the coarse-grained status of one upload request. Per-file byte
// progress is rendered on the server's terminal by up.Run; the web UI only
// tracks queued/running/done/error here.
//
// ChatID/ChatName tie the task to the conversation it was sent to, so the web
// UI can group a conversation's uploads into its own window (and lay self vs
// other-account uploads left/right). Files carries the names+sizes shown in the
// chat bubbles.
type Task struct {
	ID        string     `json:"id"`
	Namespace string     `json:"namespace"`
	Target    string     `json:"target"`
	ChatID    int64      `json:"chat_id,omitempty"`
	ChatName  string     `json:"chat_name,omitempty"`
	Paths     []string   `json:"paths"`
	Files     []TaskFile `json:"files,omitempty"`
	Caption   string     `json:"caption,omitempty"`
	Album     bool       `json:"album,omitempty"` // sent as one Telegram media group
	Progress  float64    `json:"progress"`        // 0-100, server -> Telegram upload percent
	Status    TaskStatus `json:"status"`
	Error     string     `json:"error,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// TaskStore is a simple in-memory, concurrency-safe registry of upload tasks.
type TaskStore struct {
	mu      sync.Mutex
	seq     int64
	tasks   map[string]*Task
	cancels map[string]context.CancelFunc // per-task upload cancel funcs
}

func NewTaskStore() *TaskStore {
	return &TaskStore{tasks: make(map[string]*Task), cancels: make(map[string]context.CancelFunc)}
}

func (s *TaskStore) Create(ns, target string, paths []string) *Task {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.seq++
	now := time.Now()
	t := &Task{
		ID:        strconv.FormatInt(s.seq, 10),
		Namespace: ns,
		Target:    target,
		Paths:     paths,
		Status:    TaskQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.tasks[t.ID] = t
	return t
}

func (s *TaskStore) Update(id string, fn func(*Task)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if t, ok := s.tasks[id]; ok {
		fn(t)
		t.UpdatedAt = time.Now()
	}
}

// List returns a snapshot copy of all tasks, newest first.
func (s *TaskStore) List() []*Task {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		c := *t
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// SetCancel records the cancel func for a task's in-flight upload.
func (s *TaskStore) SetCancel(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels[id] = cancel
}

// dropCancel forgets a task's cancel func (called when the upload finishes).
func (s *TaskStore) dropCancel(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels, id)
}

// Cancel aborts a task's upload (if any) and removes it from the store, so its
// chat bubble disappears. Returns true if the task existed.
func (s *TaskStore) Cancel(id string) bool {
	s.mu.Lock()
	cancel := s.cancels[id]
	_, existed := s.tasks[id]
	delete(s.tasks, id)
	delete(s.cancels, id)
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return existed
}
