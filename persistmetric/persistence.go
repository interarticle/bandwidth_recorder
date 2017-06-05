package persistmetric

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/boltdb/bolt"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricsBucketName = "persistent-metrics"
)

type Storage struct {
	db *bolt.DB

	counters []*Counter

	options *options
}

type MetricValue struct {
	Since string  `json:"since"`
	Value float64 `json:"value"`
}

type MetricValues []MetricValue

func New(opts ...Option) (*Storage, error) {
	s := &Storage{}

	s.options = defaultOptions()
	err := s.options.Update(opts...)
	if err != nil {
		return nil, err
	}

	return s, nil
}

func MustNew(opts ...Option) *Storage {
	storage, err := New(opts...)
	if err != nil {
		panic(err)
	}
	return storage
}

func (s *Storage) ListMetrics() ([]string, error) {
	if s.db == nil {
		return nil, errors.New("not initialized")
	}

	var metrics []string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.options.metricsBucketName))
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
	if s.db == nil {
		return nil, errors.New("not initialized")
	}

	var result MetricValues
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.options.metricsBucketName))
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
	if s.db == nil {
		return errors.New("not initialized")
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.options.metricsBucketName))
		data, err := json.Marshal(&values)
		if err != nil {
			return err
		}
		return b.Put([]byte(metric), data)
	})
}

// Warning: not thread safe.
func (s *Storage) NewCounter(counterOpts prometheus.Opts, opts ...Option) (*Counter, error) {
	if s.db != nil {
		return nil, errors.New("must not add new counter after initialization")
	}

	newOptions := s.options.Copy()
	err := newOptions.Update(opts...)
	if err != nil {
		return nil, err
	}
	counter := newCounter(s, counterOpts, newOptions)
	s.counters = append(s.counters, counter)
	return counter, nil
}

func (s *Storage) MustNewCounter(counterOpts prometheus.Opts, opts ...Option) *Counter {
	counter, err := s.NewCounter(counterOpts, opts...)
	if err != nil {
		panic(err)
	}
	return counter
}

func (s *Storage) Initialize(ctx context.Context, dbPath string) error {
	if s.db != nil {
		return errors.New("already initialized")
	}

	db, err := bolt.Open(dbPath, 0644, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(s.options.metricsBucketName))
		return err
	})
	if err != nil {
		return err
	}
	s.db = db

	for _, counter := range s.counters {
		err := counter.loadSavedValues()
		if err != nil {
			return err
		}
	}

	if s.options.enableAutoSave {
		go func() {
			ticker := time.NewTicker(s.options.autoSaveInterval)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					for _, counter := range s.counters {
						err := counter.saveMetrics()
						if err != nil {
							log.Printf("Warning: failed to save metrics periodically: %v", err)
						}
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	return nil
}
