package netsec

import (
	"fmt"
	"runtime"
)

type config struct {
	concurrency  int
	stageFactory StageFactory
}

// Option configures an Expander.
type Option func(*config) error

// WithConcurrency sets the maximum number of stream decoders that may run at
// once. Values below 1 are rejected by New.
func WithConcurrency(n int) Option {
	return func(c *config) error {
		if n < 1 {
			return fmt.Errorf("%w: concurrency must be at least 1", ErrInvalidConfig)
		}
		c.concurrency = n
		return nil
	}
}

// WithStageFactory overrides the staging-buffer implementation. It is useful
// for injecting short writes, storage accounting, or test instrumentation.
// The factory may be invoked concurrently and must be safe for concurrent use.
func WithStageFactory(f StageFactory) Option {
	return func(c *config) error {
		if f == nil {
			return fmt.Errorf("%w: stage factory must not be nil", ErrInvalidConfig)
		}
		c.stageFactory = f
		return nil
	}
}

func defaultConfig() config {
	n := runtime.GOMAXPROCS(0)
	if n < 4 {
		n = 4
	}
	return config{concurrency: n, stageFactory: defaultStageFactory}
}
