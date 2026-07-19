package observability

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type cbState int

const (
	cbClosed   cbState = iota // normal operation
	cbOpen                    // failing, reject requests
	cbHalfOpen                // probing recovery
)

// CircuitBreaker wraps an LLMProvider and opens when consecutive failures
// exceed the threshold. While open, requests fail fast. After the recovery
// window passes, one probe is allowed through; success closes the circuit.
type CircuitBreaker struct {
	mu          sync.Mutex
	provider    output.LLMProvider
	state       cbState
	failures    int
	threshold   int
	openUntil   time.Time
	recoveryTTL time.Duration
	name        string
}

func NewCircuitBreaker(provider output.LLMProvider, name string) *CircuitBreaker {
	return &CircuitBreaker{
		provider:    provider,
		state:       cbClosed,
		threshold:   5,
		recoveryTTL: 30 * time.Second,
		name:        name,
	}
}

func (cb *CircuitBreaker) Chat(ctx context.Context, req output.LLMRequest) (*output.LLMResponse, error) {
	if err := cb.allow(); err != nil {
		return nil, err
	}
	resp, err := cb.provider.Chat(ctx, req)
	cb.record(err)
	return resp, err
}

func (cb *CircuitBreaker) ChatStream(ctx context.Context, req output.LLMRequest, onChunk func(string)) (*output.LLMResponse, error) {
	if err := cb.allow(); err != nil {
		return nil, err
	}
	resp, err := cb.provider.ChatStream(ctx, req, onChunk)
	cb.record(err)
	return resp, err
}

func (cb *CircuitBreaker) allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case cbClosed:
		return nil
	case cbOpen:
		if time.Now().Before(cb.openUntil) {
			return fmt.Errorf("circuit breaker open for %s: service unavailable, retrying in %s",
				cb.name, time.Until(cb.openUntil).Round(time.Second))
		}
		cb.state = cbHalfOpen
		slog.Info("circuit breaker half-open, probing", "provider", cb.name)
		return nil
	case cbHalfOpen:
		return fmt.Errorf("circuit breaker half-open for %s: probe in progress", cb.name)
	}
	return nil
}

func (cb *CircuitBreaker) record(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if err == nil {
		if cb.state == cbHalfOpen {
			slog.Info("circuit breaker closed after successful probe", "provider", cb.name)
		}
		cb.state = cbClosed
		cb.failures = 0
		return
	}
	cb.failures++
	if cb.state == cbHalfOpen || cb.failures >= cb.threshold {
		cb.state = cbOpen
		cb.openUntil = time.Now().Add(cb.recoveryTTL)
		slog.Warn("circuit breaker opened",
			"provider", cb.name,
			"failures", cb.failures,
			"recover_at", cb.openUntil.Format(time.RFC3339))
	}
}
