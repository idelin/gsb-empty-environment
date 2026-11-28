package device

import "errors"

// Sentinel errors describing NVMe ZNS command failures. All of them can be
// matched with errors.Is, optionally wrapped in a *ZoneError carrying the
// offending zone id.
var (
	// ErrInvalidZoneWrite is returned when a regular Write does not start at
	// the zone's current WritePointer, crosses the ZoneCapacity boundary or is
	// directed at a zone that is not writable.
	ErrInvalidZoneWrite = errors.New("InvalidZoneWrite: write does not conform to the zone write pointer / capacity state")

	// ErrTooManyOpenZones is returned when an open slot is required and the
	// MAX_OPEN_ZONES limit cannot be satisfied (all open zones are explicitly
	// open and therefore protected from implicit eviction).
	ErrTooManyOpenZones = errors.New("TooManyOpenZones: MAX_OPEN_ZONES exhausted and no implicitly open zone can be evicted")

	// ErrTooManyActiveZones is returned when activating a new EMPTY zone would
	// exceed MAX_ACTIVE_ZONES.
	ErrTooManyActiveZones = errors.New("TooManyActiveZones: MAX_ACTIVE_ZONES exhausted")

	// ErrInvalidZoneState is returned when a management command is illegal for
	// the current zone state (e.g. resetting an EMPTY zone, closing a FULL zone).
	ErrInvalidZoneState = errors.New("InvalidZoneState: management command is not legal in the current zone state")

	// ErrZoneNotFound is returned when a zone id is out of range.
	ErrZoneNotFound = errors.New("ZoneNotFound: unknown zone id")

	// ErrLBARange is returned when an LBA is outside the namespace address space.
	ErrLBARange = errors.New("LBARange: logical block address outside namespace bounds")

	// ErrInvalidArgument is returned for malformed command parameters.
	ErrInvalidArgument = errors.New("InvalidArgument: malformed command parameter")

	// ErrZoneReadOnly is returned when a write/management transition targets a
	// READ_ONLY zone.
	ErrZoneReadOnly = errors.New("ZoneReadOnly: zone is in read only state")

	// ErrZoneOffline is returned when a command targets an OFFLINE zone.
	ErrZoneOffline = errors.New("ZoneOffline: zone is in offline state")
)

// ZoneError wraps a sentinel error with the offending zone id and a detail
// message. errors.Is still works against the sentinel.
type ZoneError struct {
	ZoneID int
	Op     string
	Err    error
}

func (e *ZoneError) Error() string {
	if e.Op == "" {
		return e.Err.Error() + " (zone " + itoa(e.ZoneID) + ")"
	}
	return e.Op + ": " + e.Err.Error() + " (zone " + itoa(e.ZoneID) + ")"
}

func (e *ZoneError) Unwrap() error { return e.Err }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func zoneErr(id int, op string, err error) error {
	return &ZoneError{ZoneID: id, Op: op, Err: err}
}
