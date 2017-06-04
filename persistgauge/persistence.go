package persistgauge

import (
	"encoding/json"
	"time"

	"github.com/boltdb/bolt"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricsBucketName = "persistent-metrics"
)

type Storage struct {
	db *bolt.DB
}

type MetricValue struct {
	Since string  `json:"since"`
	Value float64 `json:"value"`
}

type MetricValues []MetricValue

func New(path string) (*Storage, error) {
	db, err := bolt.Open(path, 0644, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(metricsBucketName))
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Storage{
		db: db,
	}, nil
}

func (s *Storage) ListMetrics() ([]string, error) {
	var metrics []string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(metricsBucketName))
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			metrics = append(metrics, string(k))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return metrics, nil
}

func (s *Storage) ReadMetric(metric string) (MetricValues, error) {
	var result MetricValues
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(metricsBucketName))
		data := b.Get([]byte(metric))
		if data == nil {
			return nil
		}

		return json.Unmarshal(data, &result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Storage) WriteMetric(metric string, values MetricValues) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(metricsBucketName))
		data, err := json.Marshal(&values)
		if err != nil {
			return err
		}
		return b.Put([]byte(metric), data)
	})
}

func (s *Storage) NewGauge(opts prometheus.GaugeOpts, options ...GaugeOption) (*Gauge, error) {
	return newGauge(s, opts, options...)
}
