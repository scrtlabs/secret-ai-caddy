package secret_reverse_proxy

import (
	"testing"
	"time"
)

func TestResilientReporterCleanup(t *testing.T) {
	// Create a test config
	config := &Config{
		Metering:         true,
		MeteringInterval: 1 * time.Second,
		MeteringURL:      "http://test.example.com",
	}
	
	// Create token accumulator and resilient reporter
	accumulator := NewTokenAccumulator()
	reporter := NewResilientReporter(config, accumulator)
	
	// Start the reporting loop
	reporter.StartReportingLoop(100 * time.Millisecond)
	
	// Wait a bit to ensure it's running
	time.Sleep(50 * time.Millisecond)
	
	// Verify it's running
	if !reporter.running {
		t.Error("Expected reporter to be running")
	}
	
	// Stop the reporter
	reporter.Stop()
	
	// Verify it stopped
	if reporter.running {
		t.Error("Expected reporter to be stopped")
	}
}

func TestMiddlewareCleanup(t *testing.T) {
	// Create middleware with resilient reporter
	m := &Middleware{
		Config: &Config{
			Metering:         true,
			MeteringInterval: 1 * time.Second,
			MeteringURL:      "http://test.example.com",
		},
		meteringRunning: true,
	}
	
	// Initialize the components (simulate Provision)
	m.tokenAccumulator = NewTokenAccumulator()
	m.resilientReporter = NewResilientReporter(m.Config, m.tokenAccumulator)
	m.resilientReporter.StartReportingLoop(100 * time.Millisecond)
	
	// Wait a bit
	time.Sleep(50 * time.Millisecond)
	
	// Verify reporter is running
	if !m.resilientReporter.running {
		t.Error("Expected resilient reporter to be running before cleanup")
	}
	
	// Call cleanup
	err := m.Cleanup()
	if err != nil {
		t.Errorf("Cleanup failed: %v", err)
	}
	
	// Verify reporter stopped
	if m.resilientReporter.running {
		t.Error("Expected resilient reporter to be stopped after cleanup")
	}
}