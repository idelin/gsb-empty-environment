package netsec

// FilterFields projects a canonical backend record onto the fields a version
// is allowed to expose, applying the version's FieldMap. Fields not mapped
// (or mapped to an empty name) are dropped, so legacy versions can never
// leak sensitive fields introduced by newer versions.
func FilterFields(v *Version, rec Record) Record {
	out := make(Record, len(rec))
	for canonical, value := range rec {
		exposed, ok := v.FieldMap[canonical]
		if !ok || exposed == "" {
			continue
		}
		out[exposed] = value
	}
	return out
}
