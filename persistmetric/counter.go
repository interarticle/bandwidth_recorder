package persistmetric

import (
	"fmt"
	"log"
	"sort"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	timeWindowStartLabelName = "since"
)

type CounterOption func(*Counter) error

func KeepNOldRecords(n int) CounterOption {
	return func(c *Counter) error {
		c.keepNOldRecords = n
		return nil
	}
}

type Counter struct {
	s        *Storage
	gaugeVec *prometheus.GaugeVec

	mu           sync.Mutex
	metricName   string
	sinceToValue map[string]float64

	keepNOldRecords int
}

func newCounter(s *Storage, opts prometheus.Opts, options ...CounterOption) (*Counter, error) {
	c := &Counter{
		s: s,
		gaugeVec: prometheus.NewGaugeVec(
			prometheus.GaugeOpts(opts), []string{timeWindowStartLabelName}),
		metricName:   fmt.Sprintf("%s::%s::%s", opts.Namespace, opts.Subsystem, opts.Name),
		sinceToValue: make(map[string]float64),

		keepNOldRecords: 2,
	}

	for _, option := range options {
		err := option(c)
		if err != nil {
			return nil, err
		}
	}

	values, err := s.ReadMetric(c.metricName)
	if err != nil {
		return nil, err
	}

	for _, value := range values {
		c.gaugeVec.WithLabelValues(value.Since).Set(value.Value)
		c.sinceToValue[value.Since] = value.Value
	}

	return c, nil
}

func (c *Counter) Describe(ch chan<- *prometheus.Desc) {
	c.gaugeVec.Describe(ch)
}

func (c *Counter) Collect(ch chan<- prometheus.Metric) {
	c.gaugeVec.Collect(ch)

	go func() {
		err := c.saveMetrics()
		if err != nil {
			log.Printf("Warning: failed to save metrics: %v", err)
		}
	}()
}

func (c *Counter) Add(since string, delta float64) {
	c.gaugeVec.WithLabelValues(since).Add(delta)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.sinceToValue[since] += delta
}

func (c *Counter) saveMetrics() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Compact map.
	var keys, dropKeys []string
	for k := range c.sinceToValue {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if len(keys) > c.keepNOldRecords {
		dropKeys = keys[0 : len(keys)-c.keepNOldRecords]
		keys = keys[len(keys)-c.keepNOldRecords : len(keys)]
	}

	for _, key := range dropKeys {
		delete(c.sinceToValue, key)
	}

	// Save metrics.
	var values MetricValues
	for _, key := range keys {
		value := MetricValue{
			Since: key,
			Value: c.sinceToValue[key],
		}
		values = append(values, value)
	}

	return c.s.WriteMetric(c.metricName, values)
}
