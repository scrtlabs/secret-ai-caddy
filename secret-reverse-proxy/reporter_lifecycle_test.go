package secret_reverse_proxy

import (
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

	if !reporter.running.Load() {
		t.Fatal("Expected reporter to be running after concurrent starts")
	}

	reporter.Stop()

	if reporter.running.Load() {
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

	if reporter.running.Load() {
		t.Error("Expected reporter to be stopped after concurrent Stop calls")
	}
}
