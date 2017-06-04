package persistgauge

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	timeWindowStartLabelName = "since"
	autoSaveInterval         = 1 * time.Minute
)

type GaugeOption func(*Gauge) error

func KeepNOldRecords(n int) GaugeOption {
	return func(g *Gauge) error {
		g.keepNOldRecords = n
		return nil
	}
}

type Gauge struct {
	s        *Storage
	gaugeVec *prometheus.GaugeVec

	mu           sync.Mutex
	metricName   string
	sinceToValue map[string]float64

	keepNOldRecords int
}

func newGauge(s *Storage, opts prometheus.GaugeOpts, options ...GaugeOption) (*Gauge, error) {
	g := &Gauge{
		s:            s,
		gaugeVec:     prometheus.NewGaugeVec(opts, []string{timeWindowStartLabelName}),
		metricName:   fmt.Sprintf("%s::%s::%s", opts.Namespace, opts.Subsystem, opts.Name),
		sinceToValue: make(map[string]float64),

		keepNOldRecords: 2,
	}

	for _, option := range options {
		err := option(g)
		if err != nil {
			return nil, err
		}
	}

	values, err := s.ReadMetric(g.metricName)
	if err != nil {
		return nil, err
	}

	for _, value := range values {
		g.gaugeVec.WithLabelValues(value.Since).Set(value.Value)
		g.sinceToValue[value.Since] = value.Value
	}

	return g, nil
}

func (g *Gauge) Describe(ch chan<- *prometheus.Desc) {
	g.gaugeVec.Describe(ch)
}

func (g *Gauge) Collect(ch chan<- prometheus.Metric) {
	g.gaugeVec.Collect(ch)

	go func() {
		err := g.saveMetrics()
		if err != nil {
			log.Printf("Warning: failed to save metrics: %v", err)
		}
	}()
}

// StartAutoSave starts a new goroutine that saves metrics once per minute.
func (g *Gauge) StartAutoSave(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(autoSaveInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				err := g.saveMetrics()
				if err != nil {
					log.Printf("Warning: failed to save metrics periodically: %v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (g *Gauge) Add(since string, delta float64) {
	g.gaugeVec.WithLabelValues(since).Add(delta)

	g.mu.Lock()
	defer g.mu.Unlock()
	g.sinceToValue[since] += delta
}

func (g *Gauge) saveMetrics() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Compact map.
	var keys, dropKeys []string
	for k := range g.sinceToValue {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if len(keys) > g.keepNOldRecords {
		dropKeys = keys[0 : len(keys)-g.keepNOldRecords]
		keys = keys[len(keys)-g.keepNOldRecords : len(keys)]
	}

	for _, key := range dropKeys {
		delete(g.sinceToValue, key)
	}

	// Save metrics.
	var values MetricValues
	for _, key := range keys {
		value := MetricValue{
			Since: key,
			Value: g.sinceToValue[key],
		}
		values = append(values, value)
	}

	return g.s.WriteMetric(g.metricName, values)
}
