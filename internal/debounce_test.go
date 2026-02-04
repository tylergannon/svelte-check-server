package internal

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestDebouncer_SingleTrigger(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var called atomic.Int32
		d := NewDebouncer(50*time.Millisecond, func() {
			called.Add(1)
		})

		d.Trigger()

		// Should not have fired yet
		time.Sleep(25 * time.Millisecond)
		synctest.Wait()
		if called.Load() != 0 {
			t.Error("callback fired too early")
		}

		// Should fire after interval
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		if called.Load() != 1 {
			t.Errorf("callback count = %d, want 1", called.Load())
		}
	})
}

func TestDebouncer_MultipleTriggers_CoalescesToOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var called atomic.Int32
		d := NewDebouncer(50*time.Millisecond, func() {
			called.Add(1)
		})

		// Rapid triggers
		d.Trigger()
		time.Sleep(20 * time.Millisecond)
		synctest.Wait()
		d.Trigger()
		time.Sleep(20 * time.Millisecond)
		synctest.Wait()
		d.Trigger()

		// Wait for interval after last trigger
		time.Sleep(75 * time.Millisecond)
		synctest.Wait()

		if called.Load() != 1 {
			t.Errorf("callback count = %d, want 1 (should coalesce)", called.Load())
		}
	})
}

func TestDebouncer_Stop_CancelsPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var called atomic.Int32
		d := NewDebouncer(50*time.Millisecond, func() {
			called.Add(1)
		})

		d.Trigger()
		time.Sleep(25 * time.Millisecond)
		synctest.Wait()
		d.Stop()

		// Wait past when it would have fired
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()

		if called.Load() != 0 {
			t.Error("callback should not have fired after Stop")
		}
	})
}

func TestDebouncer_TriggerAfterStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var called atomic.Int32
		d := NewDebouncer(50*time.Millisecond, func() {
			called.Add(1)
		})

		d.Trigger()
		d.Stop()

		// Trigger again after stop
		d.Trigger()
		time.Sleep(75 * time.Millisecond)
		synctest.Wait()

		if called.Load() != 1 {
			t.Errorf("callback count = %d, want 1", called.Load())
		}
	})
}

func TestDebouncer_TwoSeparateBursts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var called atomic.Int32
		d := NewDebouncer(50*time.Millisecond, func() {
			called.Add(1)
		})

		// First burst
		d.Trigger()
		d.Trigger()
		d.Trigger()

		time.Sleep(75 * time.Millisecond)
		synctest.Wait()

		if called.Load() != 1 {
			t.Errorf("after first burst: callback count = %d, want 1", called.Load())
		}

		// Second burst (after first has fired)
		d.Trigger()
		d.Trigger()

		time.Sleep(75 * time.Millisecond)
		synctest.Wait()

		if called.Load() != 2 {
			t.Errorf("after second burst: callback count = %d, want 2", called.Load())
		}
	})
}
