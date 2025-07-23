package metering

import (
	"fmt"
	"sync"
	"time"
	
	"github.com/caddyserver/caddy/v2"
	"go.uber.org/zap"
)

// RateLimitConfig defines rate limiting parameters
type RateLimitConfig struct {
	TokensPerMinute int           `json:"tokens_per_minute,omitempty"`
	TokensPerHour   int           `json:"tokens_per_hour,omitempty"`
	TokensPerDay    int           `json:"tokens_per_day,omitempty"`
	BurstSize       int           `json:"burst_size,omitempty"`
	WindowSize      time.Duration `json:"window_size,omitempty"`
}

// RateLimitWindow tracks usage within a time window
type RateLimitWindow struct {
	TokensUsed int
	ResetTime  time.Time
}

// TokenRateLimiter implements token-based rate limiting
type TokenRateLimiter struct {
	config   RateLimitConfig
	windows  map[string]map[string]*RateLimitWindow // [apiKeyHash][window_type]
	mu       sync.RWMutex
	logger   *zap.Logger
}

func NewTokenRateLimiter(config RateLimitConfig) *TokenRateLimiter {
	return &TokenRateLimiter{
		config:  config,
		windows: make(map[string]map[string]*RateLimitWindow),
		logger:  caddy.Log(),
	}
}

// CheckRateLimit returns whether the request should be allowed
func (trl *TokenRateLimiter) CheckRateLimit(apiKeyHash string, estimatedTokens int) error {
	trl.mu.Lock()
	defer trl.mu.Unlock()

	now := time.Now()
	
	// Initialize windows for this API key if needed
	if trl.windows[apiKeyHash] == nil {
		trl.windows[apiKeyHash] = make(map[string]*RateLimitWindow)
	}
	
	userWindows := trl.windows[apiKeyHash]
	
	// Check each configured limit
	if trl.config.TokensPerMinute > 0 {
		if err := trl.checkWindow(userWindows, "minute", trl.config.TokensPerMinute, 
			estimatedTokens, now, time.Minute); err != nil {
			return err
		}
	}
	
	if trl.config.TokensPerHour > 0 {
		if err := trl.checkWindow(userWindows, "hour", trl.config.TokensPerHour, 
			estimatedTokens, now, time.Hour); err != nil {
			return err
		}
	}
	
	if trl.config.TokensPerDay > 0 {
		if err := trl.checkWindow(userWindows, "day", trl.config.TokensPerDay, 
			estimatedTokens, now, 24*time.Hour); err != nil {
			return err
		}
	}
	
	return nil
}

// RecordUsage updates rate limit windows after successful processing
func (trl *TokenRateLimiter) RecordUsage(apiKeyHash string, actualTokens int) {
	trl.mu.Lock()
	defer trl.mu.Unlock()

	now := time.Now()
	
	if trl.windows[apiKeyHash] == nil {
		return // Should not happen if CheckRateLimit was called first
	}
	
	userWindows := trl.windows[apiKeyHash]
	
	// Update each window
	if trl.config.TokensPerMinute > 0 {
		trl.updateWindow(userWindows, "minute", actualTokens, now, time.Minute)
	}
	
	if trl.config.TokensPerHour > 0 {
		trl.updateWindow(userWindows, "hour", actualTokens, now, time.Hour)
	}
	
	if trl.config.TokensPerDay > 0 {
		trl.updateWindow(userWindows, "day", actualTokens, now, 24*time.Hour)
	}
}

func (trl *TokenRateLimiter) checkWindow(userWindows map[string]*RateLimitWindow, 
	windowType string, limit int, estimatedTokens int, now time.Time, duration time.Duration) error {
	
	window := userWindows[windowType]
	
	// Initialize or reset window if needed
	if window == nil || now.After(window.ResetTime) {
		userWindows[windowType] = &RateLimitWindow{
			TokensUsed: 0,
			ResetTime:  now.Add(duration),
		}
		window = userWindows[windowType]
	}
	
	// Check if adding estimated tokens would exceed limit
	if window.TokensUsed + estimatedTokens > limit {
		remaining := limit - window.TokensUsed
		if remaining < 0 {
			remaining = 0
		}
		
		return fmt.Errorf("rate limit exceeded for %s window: %d/%d tokens used, %d remaining, reset at %v",
			windowType, window.TokensUsed, limit, remaining, window.ResetTime)
	}
	
	return nil
}

func (trl *TokenRateLimiter) updateWindow(userWindows map[string]*RateLimitWindow, 
	windowType string, actualTokens int, now time.Time, duration time.Duration) {
	
	window := userWindows[windowType]
	if window == nil || now.After(window.ResetTime) {
		// This shouldn't happen if checkWindow was called first, but handle it gracefully
		userWindows[windowType] = &RateLimitWindow{
			TokensUsed: actualTokens,
			ResetTime:  now.Add(duration),
		}
		return
	}
	
	window.TokensUsed += actualTokens
}

// CleanupExpiredWindows removes old rate limit windows to prevent memory leaks
func (trl *TokenRateLimiter) CleanupExpiredWindows() {
	trl.mu.Lock()
	defer trl.mu.Unlock()
	
	now := time.Now()
	keysToDelete := make([]string, 0)
	
	for apiKeyHash, userWindows := range trl.windows {
		allExpired := true
		
		for windowType, window := range userWindows {
			if window != nil && now.Before(window.ResetTime) {
				allExpired = false
			} else {
				delete(userWindows, windowType)
			}
		}
		
		if allExpired || len(userWindows) == 0 {
			keysToDelete = append(keysToDelete, apiKeyHash)
		}
	}
	
	for _, key := range keysToDelete {
		delete(trl.windows, key)
	}
	
	if len(keysToDelete) > 0 {
		trl.logger.Debug("Cleaned up expired rate limit windows", 
			zap.Int("cleaned_users", len(keysToDelete)))
	}
}

// GetRateLimitStatus returns current rate limit status for debugging
func (trl *TokenRateLimiter) GetRateLimitStatus(apiKeyHash string) map[string]interface{} {
	trl.mu.RLock()
	defer trl.mu.RUnlock()
	
	status := make(map[string]interface{})
	userWindows := trl.windows[apiKeyHash]
	
	if userWindows == nil {
		return status
	}
	
	now := time.Now()
	
	for windowType, window := range userWindows {
		if window != nil && now.Before(window.ResetTime) {
			var limit int
			switch windowType {
			case "minute":
				limit = trl.config.TokensPerMinute
			case "hour":
				limit = trl.config.TokensPerHour
			case "day":
				limit = trl.config.TokensPerDay
			}
			
			status[windowType] = map[string]interface{}{
				"used":       window.TokensUsed,
				"limit":      limit,
				"remaining":  limit - window.TokensUsed,
				"reset_time": window.ResetTime,
			}
		}
	}
	
	return status
}

// StartCleanupLoop starts a background goroutine to clean up expired windows
func (trl *TokenRateLimiter) StartCleanupLoop() {
	go func() {
		ticker := time.NewTicker(time.Hour) // Clean up every hour
		defer ticker.Stop()
		
		for range ticker.C {
			trl.CleanupExpiredWindows()
		}
	}()
}