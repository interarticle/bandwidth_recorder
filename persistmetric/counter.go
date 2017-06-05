package persistmetric

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	timeWindowStartLabelName = "since"
)

type Counter struct {
	s          *Storage
	counterVec *prometheus.GaugeVec

	mu           sync.Mutex
	metricName   string
	sinceToValue map[string]float64

	options *options
}

func newCounter(s *Storage, counterOpts prometheus.Opts, opts *options) *Counter {
	return &Counter{
		s: s,
		counterVec: prometheus.NewGaugeVec(
			prometheus.GaugeOpts(counterOpts), []string{timeWindowStartLabelName}),
		metricName: fmt.Sprintf("%s::%s::%s", counterOpts.Namespace,
			counterOpts.Subsystem, counterOpts.Name),
		options: opts,
	}
}

func (c *Counter) loadSavedValues() error {
	if c.sinceToValue != nil {
		return errors.New("already initialized")
	}

	values, err := c.s.ReadMetric(c.metricName)
	if err != nil {
		return err
	}

	c.sinceToValue = make(map[string]float64)
	for _, value := range values {
		c.counterVec.WithLabelValues(value.Since).Set(value.Value)
		c.sinceToValue[value.Since] = value.Value
	}
	return nil
}

func (c *Counter) Describe(ch chan<- *prometheus.Desc) {
	c.counterVec.Describe(ch)
}

func (c *Counter) Collect(ch chan<- prometheus.Metric) {
	c.counterVec.Collect(ch)

	go func() {
		err := c.saveMetrics()
		if err != nil {
			log.Printf("Warning: failed to save metrics: %v", err)
		}
	}()
}

func (c *Counter) Add(since string, delta float64) {
	if c.sinceToValue == nil {
		panic(errors.New("not initialized"))
	}

	c.counterVec.WithLabelValues(since).Add(delta)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.sinceToValue[since] += delta
}

func (c *Counter) saveMetrics() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sinceToValue == nil {
		return errors.New("not initialized")
	}

	// Compact map.
	var keys, dropKeys []string
	for k := range c.sinceToValue {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if len(keys) > c.options.numOldRecordsToKeep {
		dropKeys = keys[0 : len(keys)-c.options.numOldRecordsToKeep]
		keys = keys[len(keys)-c.options.numOldRecordsToKeep : len(keys)]
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
