package internal

import (
	"sync"
	"time"
)

// Debouncer coalesces rapid triggers into a single callback after a quiet period.
// Each call to Trigger resets the timer. The callback fires only after the
// interval has elapsed with no new triggers.
//
// The zero value is not usable; use NewDebouncer to create a Debouncer.
type Debouncer struct {
	interval time.Duration
	callback func()

	mu    sync.Mutex
	timer *time.Timer
}

// NewDebouncer creates a new Debouncer that will call callback after interval
// has elapsed since the last Trigger call.
func NewDebouncer(interval time.Duration, callback func()) *Debouncer {
	return &Debouncer{
		interval: interval,
		callback: callback,
	}
}

// Trigger resets the debounce timer. If no further Trigger calls occur within
// the interval, the callback will be invoked.
func (d *Debouncer) Trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.interval, d.callback)
}

// Stop cancels any pending callback. It is safe to call Trigger again after Stop.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}
