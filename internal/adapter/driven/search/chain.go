// Package search composes SearchProviders. Chain tries each provider in order
// and returns the first non-empty result set, so a primary (e.g. Tavily) can
// degrade gracefully to a free fallback (SearXNG/DuckDuckGo) on error or empty.
package search

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type Chain struct {
	providers []named
}

type named struct {
	name     string
	provider output.SearchProvider
}

// NewChain builds a fallback chain. Pass providers most-preferred first.
func NewChain() *Chain { return &Chain{} }

// Add appends a provider to the chain. Nil providers are ignored, so callers can
// wire an optional primary without branching.
func (c *Chain) Add(name string, p output.SearchProvider) *Chain {
	if p != nil {
		c.providers = append(c.providers, named{name: name, provider: p})
	}
	return c
}

func (c *Chain) Search(ctx context.Context, query string, opts output.SearchOptions) ([]output.SearchResult, error) {
	if len(c.providers) == 0 {
		return nil, fmt.Errorf("search: no providers configured")
	}

	var lastErr error
	for _, p := range c.providers {
		results, err := p.provider.Search(ctx, query, opts)
		if err != nil {
			lastErr = err
			slog.Warn("search provider failed, trying next", "provider", p.name, "error", err)
			continue
		}
		if len(results) > 0 {
			return results, nil
		}
		slog.Debug("search provider returned no results, trying next", "provider", p.name)
	}

	if lastErr != nil {
		return nil, fmt.Errorf("search: all providers failed, last error: %w", lastErr)
	}
	// Every provider succeeded but returned nothing — a real empty result, not a failure.
	return []output.SearchResult{}, nil
}
