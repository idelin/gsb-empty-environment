package netsec

// Device describes a block device that callers allow a device-mapper table to
// reference. Sizes and offsets in a device-mapper table are expressed in 512-byte
// sectors.
type Device struct {
	SizeSectors uint64
	ReadOnly    bool
}

// DeviceSet is the caller's registry of block devices that a table may use as
// backing devices.
type DeviceSet = map[string]Device

// Segment is one parsed device-mapper table line.
type Segment struct {
	Start      uint64
	Length     uint64
	TargetType string
	Params     []string
}

type validateConfig struct {
	mappingName string
	readOnly    bool
}

// Option configures validation that depends on the mapping being loaded.
type Option func(*validateConfig)

// WithMappingName supplies the name of the device-mapper device being created.
// It is used to reject linear and verity targets that reference the mapping
// itself and would therefore form a cycle.
func WithMappingName(name string) Option {
	return func(cfg *validateConfig) {
		cfg.mappingName = name
	}
}

// WithTarget is an alias for WithMappingName.
func WithTarget(name string) Option {
	return WithMappingName(name)
}

// WithReadOnlyMapping marks the mapping being loaded as read-only. A read-only
// mapping may expose a read-only backing device through a linear target.
func WithReadOnlyMapping(readOnly bool) Option {
	return func(cfg *validateConfig) {
		cfg.readOnly = readOnly
	}
}

// WithReadOnly is an alias for WithReadOnlyMapping.
func WithReadOnly(readOnly bool) Option {
	return WithReadOnlyMapping(readOnly)
}
