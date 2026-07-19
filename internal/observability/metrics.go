package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var meter = otel.Meter("go-assistant")

type LLMMetrics struct {
	requestDuration metric.Float64Histogram
	inputTokens     metric.Int64Counter
	outputTokens    metric.Int64Counter
	cachedTokens    metric.Int64Counter
	errors          metric.Int64Counter
}

func NewLLMMetrics() (*LLMMetrics, error) {
	requestDuration, err := meter.Float64Histogram(
		"llm.request.duration",
		metric.WithDescription("LLM request duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	inputTokens, err := meter.Int64Counter(
		"llm.tokens.input",
		metric.WithDescription("Total input tokens sent to LLM"),
	)
	if err != nil {
		return nil, err
	}
	outputTokens, err := meter.Int64Counter(
		"llm.tokens.output",
		metric.WithDescription("Total output tokens received from LLM"),
	)
	if err != nil {
		return nil, err
	}
	cachedTokens, err := meter.Int64Counter(
		"llm.tokens.cached",
		metric.WithDescription("Input tokens served from cache (prompt caching)"),
	)
	if err != nil {
		return nil, err
	}
	errors, err := meter.Int64Counter(
		"llm.errors",
		metric.WithDescription("Total LLM request errors"),
	)
	if err != nil {
		return nil, err
	}
	return &LLMMetrics{
		requestDuration: requestDuration,
		inputTokens:     inputTokens,
		outputTokens:    outputTokens,
		cachedTokens:    cachedTokens,
		errors:          errors,
	}, nil
}

func (m *LLMMetrics) RecordRequest(ctx context.Context, provider, model string, duration time.Duration, inputTok, outputTok, cachedTok int, err error) {
	attrs := []attribute.KeyValue{
		attribute.String("provider", provider),
		attribute.String("model", model),
	}
	m.requestDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attrs...))
	if inputTok > 0 {
		m.inputTokens.Add(ctx, int64(inputTok), metric.WithAttributes(attrs...))
	}
	if outputTok > 0 {
		m.outputTokens.Add(ctx, int64(outputTok), metric.WithAttributes(attrs...))
	}
	if cachedTok > 0 {
		m.cachedTokens.Add(ctx, int64(cachedTok), metric.WithAttributes(attrs...))
	}
	if err != nil {
		m.errors.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
}

type ToolMetrics struct {
	executionDuration metric.Float64Histogram
	errors            metric.Int64Counter
}

func NewToolMetrics() (*ToolMetrics, error) {
	executionDuration, err := meter.Float64Histogram(
		"tool.execution.duration",
		metric.WithDescription("Tool execution duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	errors, err := meter.Int64Counter(
		"tool.errors",
		metric.WithDescription("Total tool execution errors"),
	)
	if err != nil {
		return nil, err
	}
	return &ToolMetrics{executionDuration: executionDuration, errors: errors}, nil
}

func (m *ToolMetrics) RecordExecution(ctx context.Context, toolName string, duration time.Duration, err error) {
	attrs := []attribute.KeyValue{attribute.String("tool", toolName)}
	m.executionDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attrs...))
	if err != nil {
		m.errors.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
}
