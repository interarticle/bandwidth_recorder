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

	variableLabels []string
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

	newOptions.variableLabels = make([]string, len(o.variableLabels))
	copy(newOptions.variableLabels, o.variableLabels)
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

// Be careful before passing this to New(), since all deriverd counters will
// then have the labels specified applied, unless explicitly overridden.
func VariableLabels(labels []string) Option {
	return func(c *options) error {
		for _, label := range labels {
			if label == "" {
				return errors.New("invalid empty label")
			}
		}
		c.variableLabels = make([]string, len(labels))
		copy(c.variableLabels, labels)
		return nil
	}
}
