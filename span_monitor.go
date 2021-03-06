package ecsevent

import (
	"sync"

	"github.com/opentracing/opentracing-go"
)

// SpanMonitor is a short-lived monitor with additional contextual fields.
// It's typically used in conjunction with a Context. It relies on a parent
// Monitor to emit.
type SpanMonitor struct {
	// SubeventsField is the field that all subevents will be recorded under.
	// If no SubeventsField is set, subevents will be recorded to emitters as
	// separate events. Has no effect on subevents emitted to an opentracing
	// span.
	SubeventsField string

	// subevents tracks all events recorded against this span monitor.
	subevents []map[string]interface{}

	// Fields are the locally scoped fields applied to all events and subevents
	// recorded by the SpanMonitor.
	fields map[string]interface{}

	// The opentracing span, if any, associated with this SpanMonitor.
	span       opentracing.Span
	parent     Monitor
	suppressed bool
	mu         *sync.RWMutex
}

var (
	// This is a compile-time check to make sure our types correctly
	// implement the interface:
	// https://medium.com/@matryer/c167afed3aae
	_ Monitor = &SpanMonitor{}
)

// SpanMonitorOption configure a GlobalMonitor as it's being initialized.
type SpanMonitorOption func(*SpanMonitor)

// WithOpenTracingSpan associates an opentracing span with the span monitor.
func WithOpenTracingSpan(span opentracing.Span) SpanMonitorOption {
	return func(sm *SpanMonitor) {
		sm.span = span
	}
}

// NewSpanMonitorFromParent creates a new
func NewSpanMonitorFromParent(m Monitor, opts ...SpanMonitorOption) *SpanMonitor {
	monitor := &SpanMonitor{
		mu:             &sync.RWMutex{},
		parent:         m,
		fields:         make(map[string]interface{}),
		subevents:      make([]map[string]interface{}, 0),
		SubeventsField: FieldEventSubevents,
	}
	for _, opts := range opts {
		opts(monitor)
	}
	// TODO: if m is also a SpanMonitor, connect the opentracing spans together if non-nil
	return monitor
}

func (sm *SpanMonitor) Fields() map[string]interface{} {
	return sm.fields
}

// Parent returns the parent logger.
func (sm *SpanMonitor) Parent() Monitor {
	return sm.parent
}

// maxDepth prevents infinite loops if a cycle is created or
// if a span monitor becomes its own parent.
const maxDepth = 100

// Root returns the root monitor for the monitor tree.
// If the top level monitor is not a RootMonitor, it will return nil.
func (sm *SpanMonitor) Root() *RootMonitor {
	monitor := sm.Parent()
	depth := 0
	for monitor != nil {
		depth++
		if depth > maxDepth {
			break
		}

		if gm, ok := monitor.(*RootMonitor); ok && gm != nil {
			return gm
		} else if sm, ok := monitor.(*SpanMonitor); ok && sm != nil {
			monitor = sm.Parent()
		} else {
			break
		}
	}
	return nil
}

// UpdateFields updates the SpanMonitor's field set.
func (sm *SpanMonitor) UpdateFields(fields map[string]interface{}) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.fields == nil {
		sm.fields = make(map[string]interface{})
	}
	for k, v := range fields {
		sm.fields[k] = v
	}
}

// Suppress causes this span monitor to emit nothing.
func (sm *SpanMonitor) Suppress() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.suppressed = true
}

// Record takes a series of fields and records an event.
func (sm *SpanMonitor) Record(event map[string]interface{}) {
	if sm.fields == nil {
		sm.mu.Lock()
		sm.fields = make(map[string]interface{})
		sm.mu.Unlock()
	}
	merged := make(map[string]interface{})
	sm.mu.RLock()
	for k, v := range sm.fields {
		merged[k] = v
	}
	sm.mu.RUnlock()
	for k, v := range event {
		merged[k] = v
	}
	sm.mu.Lock()
	sm.subevents = append(sm.subevents, merged)
	defer sm.mu.Unlock()
	if sm.suppressed {
		return
	}
	// TODO: if configured to flush immediately, emit to parent, otherwise emit on Finish
}

func (sm *SpanMonitor) Finish() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.suppressed {
		return
	}
	if sm.span != nil {
		records := make([]opentracing.LogRecord, 0)
		// TODO: generate log records for each subevent
		opts := opentracing.FinishOptions{LogRecords: records}
		sm.span.FinishWithOptions(opts)
	}
	if len(sm.subevents) > 0 {
		sm.fields[sm.SubeventsField] = sm.subevents
	}
	sm.parent.Record(sm.fields)
}
