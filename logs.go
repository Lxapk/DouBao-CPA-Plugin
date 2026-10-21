package main

import (
	"sync"
	"time"
)

// The in-memory call log behind the panel's 统计 page.
//
// CPA keeps its own request log, but it records the HTTP exchange, not what the
// plugin did with it: which account, which upstream capability, how long the
// generation took. That attribution is the whole point of this log.
//
// It is deliberately memory-only. A call log is a diagnostic surface, and writing
// every request to disk would add a file write to the hot path for data nobody
// needs after a restart.

// callEntry is one finished request.
type callEntry struct {
	Time     string `json:"time"`
	Account  string `json:"account"`
	Model    string `json:"model"`
	Realm    string `json:"realm"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Duration int64  `json:"duration_ms"`
	Detail   string `json:"detail,omitempty"`
}

// callLog is a bounded ring of recent entries.
type callLog struct {
	mu    sync.RWMutex
	limit int
	items []callEntry
}

func newCallLog(limit int) *callLog {
	if limit <= 0 {
		limit = 500
	}
	return &callLog{limit: limit}
}

func (l *callLog) add(entry callEntry) {
	if entry.Time == "" {
		entry.Time = time.Now().Format("2006-01-02 15:04:05")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, entry)
	if len(l.items) > l.limit {
		l.items = l.items[len(l.items)-l.limit:]
	}
}

// recent returns the newest entries first.
func (l *callLog) recent(max int) []callEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if max <= 0 || max > len(l.items) {
		max = len(l.items)
	}
	out := make([]callEntry, 0, max)
	for i := len(l.items) - 1; i >= 0 && len(out) < max; i-- {
		out = append(out, l.items[i])
	}
	return out
}

func (l *callLog) clear() {
	l.mu.Lock()
	l.items = nil
	l.mu.Unlock()
}
