package splitter

// ScanReference is the independent reference implementation: it walks both
// targets in full and keeps, per key, the record with the highest version.
// The splitter's pointer view (Scan) must agree with this for every key
// whose highest version was committed; tests use it as the oracle.
func ScanReference(online, archive Target) (map[string]Record, error) {
	out := map[string]Record{}
	for _, t := range []Target{online, archive} {
		recs, err := t.List()
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			cur, ok := out[rec.Key]
			if !ok || rec.Version > cur.Version {
				out[rec.Key] = rec
			}
		}
	}
	return out, nil
}
