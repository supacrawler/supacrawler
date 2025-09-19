package parse

import (
	"context"
	"fmt"
	"time"

	"scraper/internal/core/job"
	"scraper/internal/logger"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/schema"
)

// EinoTracer provides comprehensive tracing for Eino workflows using native streaming
type EinoTracer struct {
	jobService *job.JobService
	jobID      string
	log        *logger.Logger
	startTime  time.Time
	nodeSteps  int64
}

// TraceEvent represents a single trace event that will be streamed
type TraceEvent struct {
	JobID      string                 `json:"jobId"`
	Event      string                 `json:"event"`
	Component  string                 `json:"component"`
	NodeName   string                 `json:"nodeName,omitempty"`
	Timing     string                 `json:"timing"`
	Timestamp  int64                  `json:"timestamp"`
	Duration   *int64                 `json:"duration,omitempty"`
	Input      interface{}            `json:"input,omitempty"`
	Output     interface{}            `json:"output,omitempty"`
	Error      *string                `json:"error,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	StepNumber int64                  `json:"stepNumber"`
}

// NewEinoTracer creates a new Eino tracer that publishes to Redis for SSE streaming
func NewEinoTracer(jobService *job.JobService, jobID string) *EinoTracer {
	return &EinoTracer{
		jobService: jobService,
		jobID:      jobID,
		log:        logger.New("EinoTracer"),
		startTime:  time.Now(),
		nodeSteps:  0,
	}
}

// CreateGlobalHandler creates an Eino callback handler that publishes traces
func (t *EinoTracer) CreateGlobalHandler() callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(t.onNodeStart).
		OnEndFn(t.onNodeEnd).
		OnErrorFn(t.onNodeError).
		OnStartWithStreamInputFn(t.onStreamInputStart).
		OnEndWithStreamOutputFn(t.onStreamOutputEnd).
		Build()
}

func (t *EinoTracer) onNodeStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	stepNum := t.incrementStep()
	event := TraceEvent{
		JobID:      t.jobID,
		Event:      "node.start",
		Component:  string(info.Component),
		NodeName:   t.getNodeName(info),
		Timing:     "start",
		Timestamp:  time.Now().UnixMilli(),
		Input:      input,
		StepNumber: stepNum,
		Metadata:   t.extractMetadata(info),
	}
	t.publishTrace(ctx, event)
	return context.WithValue(ctx, t.contextKey(info), time.Now())
}

func (t *EinoTracer) onNodeEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	stepNum := t.incrementStep()
	var duration *int64
	if startTime, ok := ctx.Value(t.contextKey(info)).(time.Time); ok {
		dur := time.Since(startTime).Milliseconds()
		duration = &dur
	}
	event := TraceEvent{
		JobID:      t.jobID,
		Event:      "node.end",
		Component:  string(info.Component),
		NodeName:   t.getNodeName(info),
		Timing:     "end",
		Timestamp:  time.Now().UnixMilli(),
		Duration:   duration,
		Output:     output,
		StepNumber: stepNum,
		Metadata:   t.extractMetadata(info),
	}
	t.publishTrace(ctx, event)
	return ctx
}

func (t *EinoTracer) onNodeError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	stepNum := t.incrementStep()
	t.log.LogErrorf("[trace] node.error name=%s component=%s err=%v", t.getNodeName(info), string(info.Component), err)
	var duration *int64
	if startTime, ok := ctx.Value(t.contextKey(info)).(time.Time); ok {
		dur := time.Since(startTime).Milliseconds()
		duration = &dur
	}
	errStr := err.Error()
	event := TraceEvent{
		JobID:      t.jobID,
		Event:      "node.error",
		Component:  string(info.Component),
		NodeName:   t.getNodeName(info),
		Timing:     "error",
		Timestamp:  time.Now().UnixMilli(),
		Duration:   duration,
		Error:      &errStr,
		StepNumber: stepNum,
		Metadata:   t.extractMetadata(info),
	}
	t.publishTrace(ctx, event)
	return ctx
}

func (t *EinoTracer) onStreamInputStart(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	stepNum := t.incrementStep()
	event := TraceEvent{
		JobID:      t.jobID,
		Event:      "stream.input_start",
		Component:  string(info.Component),
		NodeName:   t.getNodeName(info),
		Timing:     "stream_start",
		Timestamp:  time.Now().UnixMilli(),
		StepNumber: stepNum,
		Metadata:   t.extractMetadata(info),
	}
	t.publishTrace(ctx, event)
	return ctx
}

func (t *EinoTracer) onStreamOutputEnd(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	stepNum := t.incrementStep()
	event := TraceEvent{
		JobID:      t.jobID,
		Event:      "stream.output_end",
		Component:  string(info.Component),
		NodeName:   t.getNodeName(info),
		Timing:     "stream_end",
		Timestamp:  time.Now().UnixMilli(),
		StepNumber: stepNum,
		Metadata:   t.extractMetadata(info),
	}
	t.publishTrace(ctx, event)
	return ctx
}

func (t *EinoTracer) publishTrace(ctx context.Context, event TraceEvent) {
	if t.jobService == nil {
		t.log.LogWarnf("[EinoTracer] Cannot publish trace - jobService is nil for job %s", t.jobID)
		return
	}

	// FILTER: Only publish essential user progress events, not internal Eino workflow noise
	// Skip all internal Eino events (node.start, node.end, stream.input_start, stream.output_end)
	switch event.Event {
	case "node.start", "node.end", "stream.input_start", "stream.output_end":
		// Skip internal Eino workflow events - they create noise
		return
	case "parse.analyzing", "parse.processing", "parse.aggregating":
		// Allow essential user progress events from publishProgressEvent
		// These are manually published and should reach the frontend
	case "node.error":
		// Only publish actual errors, not internal noise
		// Continue to publish error events
	default:
		// Allow any non-internal events but log what we're seeing
		t.log.LogDebugf("[EinoTracer] Unknown event type: %s (allowing through)", event.Event)
	}

	t.log.LogDebugf("[EinoTracer] Publishing essential trace event for job %s: event=%s component=%s timing=%s",
		t.jobID, event.Event, event.Component, event.Timing)

	err := t.jobService.PublishJobTrace(ctx, t.jobID, event)
	if err != nil {
		t.log.LogErrorf("[EinoTracer] Failed to publish trace event for job %s: %v", t.jobID, err)
	}
}

func (t *EinoTracer) incrementStep() int64 {
	t.nodeSteps++
	return t.nodeSteps
}

func (t *EinoTracer) getNodeName(info *callbacks.RunInfo) string {
	if info != nil && info.Name != "" {
		return info.Name
	}
	return string(info.Component)
}

func (t *EinoTracer) contextKey(info *callbacks.RunInfo) string {
	return fmt.Sprintf("trace_start_%s_%s", info.Component, t.getNodeName(info))
}

func (t *EinoTracer) extractMetadata(info *callbacks.RunInfo) map[string]interface{} {
	return map[string]interface{}{
		"component_type": string(info.Component),
		"node_name":      info.Name,
	}
}
