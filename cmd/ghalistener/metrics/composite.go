package metrics

import (
	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
)

var _ listener.MetricsRecorder = &CompositeRecorder{}

// CompositeRecorder delegates every MetricsRecorder call to all
// wrapped recorders. This allows the Prometheus exporter and OTel
// recorder to operate side by side.
type CompositeRecorder struct {
	recorders []listener.MetricsRecorder
}

// NewComposite creates a MetricsRecorder that fans out to all given recorders.
func NewComposite(recorders ...listener.MetricsRecorder) *CompositeRecorder {
	return &CompositeRecorder{recorders: recorders}
}

func (c *CompositeRecorder) RecordStatistics(stats *scaleset.RunnerScaleSetStatistic) {
	for _, r := range c.recorders {
		r.RecordStatistics(stats)
	}
}

func (c *CompositeRecorder) RecordJobStarted(msg *scaleset.JobStarted) {
	for _, r := range c.recorders {
		r.RecordJobStarted(msg)
	}
}

func (c *CompositeRecorder) RecordJobCompleted(msg *scaleset.JobCompleted) {
	for _, r := range c.recorders {
		r.RecordJobCompleted(msg)
	}
}

func (c *CompositeRecorder) RecordDesiredRunners(count int) {
	for _, r := range c.recorders {
		r.RecordDesiredRunners(count)
	}
}
