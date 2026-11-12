package netsec

const (
	// StripeSize is the fixed erase granularity: one mebibyte (1 MiB).
	StripeSize int64 = 1 << 20
	// DefaultMaxAuthFailures is used when no limit is configured.
	DefaultMaxAuthFailures = 3
	// DefaultPattern is the fixed byte written to every erased stripe.
	DefaultPattern byte = 0x00
)

// SyncMode controls durability of backing-device stripe writes.
type SyncMode uint8

const (
	// SyncEachStripe fsyncs every stripe write.  The zero value is the safe
	// default so a zero Config still has power-loss-safe semantics.
	SyncEachStripe SyncMode = iota
	// SyncNone skips per-stripe fsync and is only suitable for tests or
	// devices that provide their own persistence guarantees.
	SyncNone
)

// Config configures a Manager.
//
// Dir is the directory in which the default journal and region files are
// stored.  A caller may instead provide JournalPath and Device explicitly.
type Config struct {
	Dir             string
	JournalPath     string
	Device          Device
	MaxAuthFailures int
	Pattern         byte
	SyncMode        SyncMode
	AutoRecover     bool
	Faults          *Faults
	// CredentialKey, when set, turns credential hashes into keyed HMACs.  It
	// must be supplied again after a restart.  Without it SHA-256 is used.
	CredentialKey []byte
}

// DefaultConfig returns a Config rooted at dir with safe defaults.
func DefaultConfig(dir string) Config {
	return Config{
		Dir:             dir,
		MaxAuthFailures: DefaultMaxAuthFailures,
		Pattern:         DefaultPattern,
		SyncMode:        SyncEachStripe,
	}
}

// Option modifies a Config passed to NewManager.
type Option func(*Config)

// WithDevice supplies a custom backing device.
func WithDevice(d Device) Option {
	return func(c *Config) { c.Device = d }
}

// WithJournalPath overrides the default erase-journal location.
func WithJournalPath(path string) Option {
	return func(c *Config) { c.JournalPath = path }
}

// WithMaxAuthFailures sets the number of consecutive credential failures that
// locks a region until restart.
func WithMaxAuthFailures(n int) Option {
	return func(c *Config) { c.MaxAuthFailures = n }
}

// WithPattern changes the fixed byte written to each erased stripe.
func WithPattern(p byte) Option {
	return func(c *Config) { c.Pattern = p }
}

// WithSyncMode sets the backing-device durability mode.
func WithSyncMode(m SyncMode) Option {
	return func(c *Config) { c.SyncMode = m }
}

// WithoutStripeSync disables per-stripe fsync in the default file device.
func WithoutStripeSync() Option {
	return func(c *Config) { c.SyncMode = SyncNone }
}

// WithAutoRecover makes NewManager resume all interrupted erases.
func WithAutoRecover() Option {
	return func(c *Config) { c.AutoRecover = true }
}

// WithFaults installs deterministic power-cut/I/O fault hooks.
func WithFaults(f Faults) Option {
	return func(c *Config) { c.Faults = &f }
}

// WithCredentialKey supplies a key for HMAC-based credential storage.
func WithCredentialKey(key []byte) Option {
	return func(c *Config) { c.CredentialKey = append([]byte(nil), key...) }
}

// Faults are deterministic injection points used to prove crash safety.
//
// Each hook may return ErrPowerLoss (or any error) to stop the erase.  The
// manager persists the transition before calling the corresponding hook where
// applicable, so recovery observes exactly the state a real power cut would
// leave behind.  Hooks must not call back into the Manager.
type Faults struct {
	BeforeStripe    func(regionID string, stripe int64) error
	AfterWrite      func(regionID string, stripe int64) error
	AfterVerify     func(regionID string, stripe int64) error
	BeforeMarkClean func(regionID string) error
	BeforeRecover   func(regionID string) error
}
