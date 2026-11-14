package netsec

import (
	"crypto/rand"
	"io"
	"time"
)

const (
	DefaultPollInterval      = 5 * time.Second
	DefaultDeviceCodeTTL     = 10 * time.Minute
	DefaultTokenTTL          = time.Hour
	DefaultUserCodeLength    = 8
	DefaultDeviceCodeEntropy = 32
)

// Config controls the lifetime and polling behavior of device authorizations.
// Zero-valued fields use their corresponding Default value.
type Config struct {
	PollInterval      time.Duration
	DeviceCodeTTL     time.Duration
	TokenTTL          time.Duration
	UserCodeLength    int
	DeviceCodeEntropy int
	Now               func() time.Time
	Random            io.Reader
}

// Option modifies a Config passed to New.
type Option func(*Config)

// Clock supplies the current time.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

// Now calls f.
func (f ClockFunc) Now() time.Time {
	return f()
}

// WithPollInterval sets the minimum interval between token endpoint polls.
func WithPollInterval(interval time.Duration) Option {
	return func(config *Config) {
		config.PollInterval = interval
	}
}

// WithDeviceCodeTTL sets how long a device authorization remains valid.
func WithDeviceCodeTTL(ttl time.Duration) Option {
	return func(config *Config) {
		config.DeviceCodeTTL = ttl
	}
}

// WithTokenTTL sets the lifetime of issued access tokens.
func WithTokenTTL(ttl time.Duration) Option {
	return func(config *Config) {
		config.TokenTTL = ttl
	}
}

// WithClock sets the clock used for expiry and polling decisions.
func WithClock(clock Clock) Option {
	return func(config *Config) {
		if clock != nil {
			config.Now = clock.Now
		}
	}
}

// WithNow sets the current-time function used by the service.
func WithNow(now func() time.Time) Option {
	return func(config *Config) {
		if now != nil {
			config.Now = now
		}
	}
}

// WithRandom sets the entropy source used for codes and access tokens.
func WithRandom(random io.Reader) Option {
	return func(config *Config) {
		if random != nil {
			config.Random = random
		}
	}
}

// DefaultConfig returns a Config populated with production-safe defaults.
func DefaultConfig() Config {
	return Config{
		PollInterval:      DefaultPollInterval,
		DeviceCodeTTL:     DefaultDeviceCodeTTL,
		TokenTTL:          DefaultTokenTTL,
		UserCodeLength:    DefaultUserCodeLength,
		DeviceCodeEntropy: DefaultDeviceCodeEntropy,
		Now:               time.Now,
		Random:            rand.Reader,
	}
}

// New creates a device authorization service.
//
// Options may be Config values, *Config values, Option values, Clock values,
// time functions, or io.Reader values.
func New(options ...any) *Service {
	config := DefaultConfig()
	for _, option := range options {
		applyOption(&config, option)
	}
	normalizeConfig(&config)
	return &Service{
		config:       config,
		byDeviceCode: make(map[string]*authorization),
		byUserCode:   make(map[string]*authorization),
	}
}

// NewWithConfig creates a service from a Config.
func NewWithConfig(config Config) *Service {
	return New(config)
}

// NewService is an alias for New.
func NewService(options ...any) *Service {
	return New(options...)
}

func applyOption(config *Config, option any) {
	switch value := option.(type) {
	case nil:
		return
	case Config:
		mergeConfig(config, value)
	case *Config:
		if value != nil {
			mergeConfig(config, *value)
		}
	case Option:
		if value != nil {
			value(config)
		}
	case Clock:
		if value != nil {
			config.Now = value.Now
		}
	case func() time.Time:
		if value != nil {
			config.Now = value
		}
	case io.Reader:
		if value != nil {
			config.Random = value
		}
	}
}

func mergeConfig(config *Config, override Config) {
	if override.PollInterval != 0 {
		config.PollInterval = override.PollInterval
	}
	if override.DeviceCodeTTL != 0 {
		config.DeviceCodeTTL = override.DeviceCodeTTL
	}
	if override.TokenTTL != 0 {
		config.TokenTTL = override.TokenTTL
	}
	if override.UserCodeLength != 0 {
		config.UserCodeLength = override.UserCodeLength
	}
	if override.DeviceCodeEntropy != 0 {
		config.DeviceCodeEntropy = override.DeviceCodeEntropy
	}
	if override.Now != nil {
		config.Now = override.Now
	}
	if override.Random != nil {
		config.Random = override.Random
	}
}

func normalizeConfig(config *Config) {
	if config.PollInterval <= 0 {
		config.PollInterval = DefaultPollInterval
	}
	if config.DeviceCodeTTL <= 0 {
		config.DeviceCodeTTL = DefaultDeviceCodeTTL
	}
	if config.TokenTTL <= 0 {
		config.TokenTTL = DefaultTokenTTL
	}
	if config.UserCodeLength < 4 {
		config.UserCodeLength = DefaultUserCodeLength
	}
	if config.DeviceCodeEntropy < 16 {
		config.DeviceCodeEntropy = DefaultDeviceCodeEntropy
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
}
