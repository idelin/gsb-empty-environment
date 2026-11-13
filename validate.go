package netsec

import (
	"math"
	"strings"
)

// ValidateTable parses and validates a device-mapper table before it is passed
// to DM_TABLE_LOAD.
func ValidateTable(raw string, devices DeviceSet, opts ...Option) error {
	segments, err := ParseTable(raw)
	if err != nil {
		return err
	}
	return ValidateSegments(segments, devices, opts...)
}

// Validate parses and validates a device-mapper table. It is an alias for
// ValidateTable.
func Validate(raw string, devices DeviceSet, opts ...Option) error {
	return ValidateTable(raw, devices, opts...)
}

// ValidateSegments validates already-parsed table segments.
func ValidateSegments(segments []Segment, devices DeviceSet, opts ...Option) error {
	if len(segments) == 0 {
		return ErrEmptyTable
	}

	cfg := validateConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	var total uint64
	for i := range segments {
		segment := &segments[i]
		if segment.Length == 0 {
			return segmentError(ErrZeroLength, i, "start=%d", segment.Start)
		}
		if segment.Start != total {
			if segment.Start > total {
				return segmentError(ErrNotContiguous, i,
					"start=%d leaves a hole after sector %d", segment.Start, total)
			}
			return segmentError(ErrNotContiguous, i,
				"start=%d overlaps the previous target ending at sector %d", segment.Start, total)
		}
		if segment.Length > math.MaxUint64-total {
			return segmentError(ErrTotalOverflow, i,
				"adding length %d to %d overflows uint64", segment.Length, total)
		}
		total += segment.Length

		if err := validateSegment(segment, i, devices, cfg); err != nil {
			return err
		}
	}
	return nil
}

func validateSegment(segment *Segment, index int, devices DeviceSet, cfg validateConfig) error {
	switch segment.TargetType {
	case "linear":
		return validateLinear(segment, index, devices, cfg)
	case "crypt":
		return validateCrypt(segment, index)
	case "verity":
		return validateVerity(segment, index, devices, cfg)
	case "thin":
		if len(segment.Params) < 2 {
			return segmentError(ErrInvalidParameters, index, "thin requires a pool device and a thin id")
		}
	case "thin-pool":
		if len(segment.Params) < 4 {
			return segmentError(ErrInvalidParameters, index,
				"thin-pool requires metadata and data devices, a block size, and a low-water mark")
		}
	default:
		return segmentError(ErrUnsupportedTarget, index, "%q", segment.TargetType)
	}
	return nil
}

func validateLinear(segment *Segment, index int, devices DeviceSet, cfg validateConfig) error {
	if len(segment.Params) < 2 {
		return segmentError(ErrInvalidParameters, index, "linear requires a device and an offset")
	}

	offset, err := parseUint(segment.Params[1])
	if err != nil {
		return segmentError(ErrInvalidParameters, index, "invalid linear offset %q", segment.Params[1])
	}

	if err := validateReference(segment.Params[0], offset, segment.Length, index, devices, cfg); err != nil {
		return err
	}
	if !cfg.readOnly && devices[segment.Params[0]].ReadOnly {
		return segmentError(ErrReadOnlyDevice, index,
			"writable linear target references read-only device %q", segment.Params[0])
	}
	return nil
}

func validateVerity(segment *Segment, index int, devices DeviceSet, cfg validateConfig) error {
	if len(segment.Params) < 2 {
		return segmentError(ErrInvalidParameters, index, "verity requires a data device and a hash device")
	}

	dataDevice := segment.Params[0]
	hashDevice := segment.Params[1]
	hasVersion := false
	if len(segment.Params) >= 3 {
		if _, err := parseUint(segment.Params[0]); err == nil {
			hasVersion = true
			dataDevice = segment.Params[1]
			hashDevice = segment.Params[2]
		}
	}

	if err := validateReference(dataDevice, 0, segment.Length, index, devices, cfg); err != nil {
		return err
	}
	if err := validateReference(hashDevice, 0, 0, index, devices, cfg); err != nil {
		return err
	}
	if hasVersion && len(segment.Params) >= 7 {
		if err := validateVerityHashRange(hashDevice, segment.Params[4], segment.Params[6], index,
			devices[hashDevice].SizeSectors); err != nil {
			return err
		}
	}
	return nil
}

func validateVerityHashRange(device, blockSizeValue, startBlockValue string, index int, size uint64) error {
	blockSize, err := parseUint(blockSizeValue)
	if err != nil || blockSize == 0 || blockSize%512 != 0 {
		return segmentError(ErrInvalidParameters, index, "invalid verity hash block size %q", blockSizeValue)
	}
	startBlock, err := parseUint(startBlockValue)
	if err != nil {
		return segmentError(ErrInvalidParameters, index, "invalid verity hash start block %q", startBlockValue)
	}

	sectorsPerBlock := blockSize / 512
	if startBlock != 0 && startBlock > math.MaxUint64/sectorsPerBlock {
		return segmentError(ErrOutOfBounds, index,
			"verity hash offset for %q overflows uint64", device)
	}
	offset := startBlock * sectorsPerBlock
	if offset > size || 1 > size-offset {
		return segmentError(ErrOutOfBounds, index,
			"verity hash range for %q starts at sector %d but device has %d sectors",
			device, offset, size)
	}
	return nil
}

func validateReference(name string, offset, length uint64, index int, devices DeviceSet, cfg validateConfig) error {
	if cfg.mappingName != "" && sameMappingDevice(name, cfg.mappingName) {
		return segmentError(ErrCycle, index, "reference to %q maps back to the target", name)
	}

	device, ok := devices[name]
	if !ok {
		return segmentError(ErrUnknownDevice, index, "%q", name)
	}
	if offset > device.SizeSectors || length > device.SizeSectors-offset {
		return segmentError(ErrOutOfBounds, index,
			"%q with offset %d and length %d exceeds size %d sectors",
			name, offset, length, device.SizeSectors)
	}
	return nil
}

func sameMappingDevice(reference, mapping string) bool {
	reference = strings.TrimSpace(reference)
	mapping = strings.TrimSpace(mapping)
	if reference == mapping {
		return true
	}
	const mapperPrefix = "/dev/mapper/"
	reference = strings.TrimPrefix(reference, mapperPrefix)
	mapping = strings.TrimPrefix(mapping, mapperPrefix)
	return reference == mapping
}
