package daemon

import (
	"sync"
	"time"
)

// debouncer coalesces repeated schedule() calls for the same key into a
// single Ready send, delay after the LAST call for that key — a file being
// actively appended to fires many Write events in quick succession, and
// re-ingesting on every single one would be wasteful (and, since each
// ingest.Run does its own byte-offset bookkeeping, mostly redundant work).
type debouncer struct {
	delay time.Duration
	ready chan string

	mu     sync.Mutex
	timers map[string]*time.Timer
}

func newDebouncer(delay time.Duration) *debouncer {
	return &debouncer{
		delay:  delay,
		ready:  make(chan string, 64),
		timers: map[string]*time.Timer{},
	}
}

// schedule (re)starts key's timer. If key already has a pending timer, that
// timer is reset — repeated calls before delay elapses keep pushing the
// fire time out rather than queuing multiple sends.
func (d *debouncer) schedule(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.timers[key]; ok {
		t.Stop()
	}
	d.timers[key] = time.AfterFunc(d.delay, func() {
		d.mu.Lock()
		delete(d.timers, key)
		d.mu.Unlock()
		d.ready <- key
	})
}

// stop cancels every pending timer without firing it. Used on shutdown so a
// timer doesn't fire (and block trying to send on d.ready) after the
// daemon's event loop has stopped reading from it.
func (d *debouncer) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, t := range d.timers {
		t.Stop()
	}
}
