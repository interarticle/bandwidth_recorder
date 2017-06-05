package persistmetric

import (
	"bytes"
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
	sinceToValue map[string]map[string]*MetricValue

	options *options
}

type CounterWithLabels struct {
	c *Counter

	labelValues  []string
	userLabelKey string
}

func (cl *CounterWithLabels) Add(since string, delta float64) {
	labelValues := append(cl.labelValues, since)
	cl.c.counterVec.WithLabelValues(labelValues...).Add(delta)

	cl.c.mu.Lock()
	defer cl.c.mu.Unlock()
	var sinceMap map[string]*MetricValue
	var ok bool
	if sinceMap, ok = cl.c.sinceToValue[since]; !ok {
		sinceMap = make(map[string]*MetricValue)
		cl.c.sinceToValue[since] = sinceMap
	}
	var value *MetricValue
	if value, ok = sinceMap[cl.userLabelKey]; !ok {
		value = &MetricValue{
			Since:  since,
			Value:  0,
			Labels: cl.labelValues,
		}
		sinceMap[cl.userLabelKey] = value
	}
	value.Value += delta
}

func userLabelKey(labels []string) string {
	var buffer bytes.Buffer
	for _, label := range labels {
		buffer.WriteString(fmt.Sprintf("%d ", len(label)))
		buffer.WriteString(label)
	}
	return buffer.String()
}

func newCounter(s *Storage, counterOpts prometheus.Opts, opts *options) *Counter {
	allLabels := append(opts.variableLabels, timeWindowStartLabelName)
	return &Counter{
		s: s,
		counterVec: prometheus.NewGaugeVec(
			prometheus.GaugeOpts(counterOpts), allLabels),
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

	c.sinceToValue = make(map[string]map[string]*MetricValue)
	for _, value := range values {
		labelValues := append(value.Labels, value.Since)
		c.counterVec.WithLabelValues(labelValues...).Set(value.Value)

		var sinceMap map[string]*MetricValue
		var ok bool
		if sinceMap, ok = c.sinceToValue[value.Since]; !ok {
			sinceMap = make(map[string]*MetricValue)
			c.sinceToValue[value.Since] = sinceMap
		}
		sinceMap[userLabelKey(value.Labels)] = &value
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

func (c *Counter) WithLabelValues(labelValues ...string) *CounterWithLabels {
	if c.sinceToValue == nil {
		panic(errors.New("not initialized"))
	}

	newLabelValues := make([]string, len(labelValues))
	copy(newLabelValues, labelValues)
	return &CounterWithLabels{
		c:            c,
		labelValues:  newLabelValues,
		userLabelKey: userLabelKey(labelValues),
	}
}

func (c *Counter) Add(since string, delta float64) {
	c.WithLabelValues().Add(since, delta)
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
		for _, value := range c.sinceToValue[key] {
			values = append(values, *value)
		}
	}

	return c.s.WriteMetric(c.metricName, values)
}
