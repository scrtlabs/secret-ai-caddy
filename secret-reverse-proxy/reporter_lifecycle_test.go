package secret_reverse_proxy

import (
	"math/rand"
	"sync"
	"testing"
	"time"
)

// TestConcurrentStartReportingLoop calls StartReportingLoop from many
// goroutines at once. Only one loop must actually start; a subsequent
// Stop must terminate it cleanly with no panic.
func TestConcurrentStartReportingLoop(t *testing.T) {
	config := &Config{
		Metering:         true,
		MeteringInterval: 1 * time.Second,
		MeteringURL:      "http://test.example.com",
	}
	accumulator := NewTokenAccumulator()
	reporter := NewResilientReporter(config, accumulator)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reporter.StartReportingLoop(10 * time.Millisecond)
		}()
	}
	wg.Wait()

	time.Sleep(50 * time.Millisecond)

	if !reporter.isRunning() {
		t.Fatal("Expected reporter to be running after concurrent starts")
	}

	reporter.Stop()

	if reporter.isRunning() {
		t.Error("Expected reporter to be stopped after Stop")
	}
}

// TestConcurrentStop calls Stop from many goroutines at once on a single
// running reporter. This must not panic from a double close of stopChan.
func TestConcurrentStop(t *testing.T) {
	config := &Config{
		Metering:         true,
		MeteringInterval: 1 * time.Second,
		MeteringURL:      "http://test.example.com",
	}
	accumulator := NewTokenAccumulator()
	reporter := NewResilientReporter(config, accumulator)

	reporter.StartReportingLoop(10 * time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reporter.Stop()
		}()
	}
	wg.Wait()

	if reporter.isRunning() {
		t.Error("Expected reporter to be stopped after concurrent Stop calls")
	}
}

// TestStopWaitsForGoroutineExit verifies that Stop only returns once the
// reporting goroutine has actually exited, rather than racing ahead of it.
func TestStopWaitsForGoroutineExit(t *testing.T) {
	config := &Config{
		Metering:         true,
		MeteringInterval: 1 * time.Second,
		MeteringURL:      "http://test.example.com",
	}
	accumulator := NewTokenAccumulator()
	reporter := NewResilientReporter(config, accumulator)

	reporter.StartReportingLoop(10 * time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	reporter.Stop()

	// By the time Stop returns, doneChan must already be closed.
	select {
	case <-reporter.doneChan:
		// expected: goroutine has exited
	default:
		t.Fatal("Expected doneChan to be closed once Stop returns")
	}
}

// TestStartReportingLoopAfterStopDoesNotRestart verifies that calling
// StartReportingLoop after Stop is a safe no-op rather than a panic. The
// reporter is single-use: stopChan/doneChan are closed once and never
// recreated, so a second start must be refused, not attempted.
func TestStartReportingLoopAfterStopDoesNotRestart(t *testing.T) {
	config := &Config{
		Metering:         true,
		MeteringInterval: 1 * time.Second,
		MeteringURL:      "http://test.example.com",
	}
	accumulator := NewTokenAccumulator()
	reporter := NewResilientReporter(config, accumulator)

	reporter.StartReportingLoop(10 * time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	reporter.Stop()

	// This must not panic (e.g. from closing an already-closed doneChan).
	reporter.StartReportingLoop(10 * time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	if reporter.isRunning() {
		t.Error("Expected reporter to remain stopped after restart attempt")
	}

	// doneChan must still be closed (no new goroutine replaced/reused it).
	select {
	case <-reporter.doneChan:
		// expected: still closed from the original Stop
	default:
		t.Fatal("Expected doneChan to remain closed after restart attempt")
	}
}

// TestMixedStartStopHammer fires a mix of StartReportingLoop and Stop calls
// from many goroutines at once against a single reporter. This pins the
// idle/running/stopped state machine's TOCTOU-free guarantee: no matter how
// Start and Stop interleave, at most one reporting goroutine is ever
// launched, and the goroutine's `defer close(rr.doneChan)` never runs twice
// (a double close panics, which would fail this test even without an
// explicit assertion).
func TestMixedStartStopHammer(t *testing.T) {
	config := &Config{
		Metering:         true,
		MeteringInterval: 1 * time.Second,
		MeteringURL:      "http://test.example.com",
	}
	accumulator := NewTokenAccumulator()
	reporter := NewResilientReporter(config, accumulator)

	const workers = 50
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			// Deliberately mix Start and Stop calls, with a small random
			// jitter so ordering across goroutines varies between runs.
			time.Sleep(time.Duration(rand.Intn(200)) * time.Microsecond)
			if i%2 == 0 {
				reporter.StartReportingLoop(5 * time.Millisecond)
			} else {
				reporter.Stop()
			}
		}(i)
	}
	wg.Wait()

	// Bring the reporter to a known-stopped state regardless of how the
	// hammer above left it (it may or may not have ever started running).
	reporter.Stop()

	if got := reporter.launchCount.Load(); got > 1 {
		t.Errorf("expected at most one reporting goroutine ever launched, got %d", got)
	}
}
