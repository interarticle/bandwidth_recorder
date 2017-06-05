package persistmetric

import (
	"errors"
	"time"
)

type options struct {
	numOldRecordsToKeep int

	enableAutoSave   bool
	autoSaveInterval time.Duration

	metricsBucketName string
}

func defaultOptions() *options {
	return &options{
		numOldRecordsToKeep: 2,

		enableAutoSave:   true,
		autoSaveInterval: 1 * time.Minute,

		metricsBucketName: "persistent-metrics",
	}
}

func (o *options) Copy() *options {
	newOptions := &options{}

	*newOptions = *o
	return newOptions
}

func (o *options) Update(opts ...Option) error {
	for _, option := range opts {
		err := option(o)
		if err != nil {
			return err
		}
	}
	return nil
}

type Option func(*options) error

func KeepNOldRecords(n int) Option {
	return func(c *options) error {
		c.numOldRecordsToKeep = n
		return nil
	}
}

func AutoSave(enable bool, interval time.Duration) Option {
	return func(c *options) error {
		c.enableAutoSave = enable
		c.autoSaveInterval = interval
		return nil
	}
}

func BucketName(name string) Option {
	return func(c *options) error {
		if name == "" {
			return errors.New("invalid bucket name")
		}
		c.metricsBucketName = name
		return nil
	}
}
