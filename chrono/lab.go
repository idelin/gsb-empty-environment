package chrono

// LabZones returns a synthetic three-version timezone table for the zone
// "Lab/Zone", designed to exercise multi-rollback migrations:
//
//	v1: UTC+1 standard; spring forward to UTC+2 on 2024-03-31T01:00Z,
//	    fall back to UTC+1 on 2024-10-27T01:00Z (overlap 02:00-03:00 local).
//	v2: same spring forward, but the fall back moves one week later to
//	    2024-11-03T01:00Z, so 2024-10-27 02:30 local is no longer ambiguous.
//	v3: DST abolished; fixed UTC+1 with no transitions.
//
// The table is synthetic and self-contained; no host timezone database is
// consulted.
func LabZones() []Zone {
	at := func(y, m, d, h int) int64 {
		return daysFromCivil(y, m, d)*86400000000000 + int64(h)*3600000000000
	}
	return []Zone{
		{
			Name: "Lab/Zone", Version: "v1", BaseOffset: 3600,
			Transitions: []Transition{
				{AtNanos: at(2024, 3, 31, 1), OffsetSec: 7200},
				{AtNanos: at(2024, 10, 27, 1), OffsetSec: 3600},
			},
		},
		{
			Name: "Lab/Zone", Version: "v2", BaseOffset: 3600,
			Transitions: []Transition{
				{AtNanos: at(2024, 3, 31, 1), OffsetSec: 7200},
				{AtNanos: at(2024, 11, 3, 1), OffsetSec: 3600},
			},
		},
		{
			Name: "Lab/Zone", Version: "v3", BaseOffset: 3600,
			Transitions: nil,
		},
	}
}

// LabRegistry is a Registry containing all Lab/Zone versions.
func LabRegistry() *Registry {
	return NewRegistry(LabZones()...)
}
