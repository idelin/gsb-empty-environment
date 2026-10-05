package pagefile

import "fmt"

// ValidationError is returned by every query entry point when Check fails.
// It carries the full Report so callers can inspect the minimal bad page
// set instead of silently reading around corrupt pages.
type ValidationError struct {
	Report *Report
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("page file failed validation (%d violations, bad pages %v); refusing to serve query",
		len(e.Report.Violations), e.Report.BadPages)
}

// checked runs the checker and fails the query if anything is wrong. No
// query path may fall back to "skip the bad pages and continue": a failed
// validation is always surfaced.
func checked(f *File) error {
	if rep := Check(f); !rep.OK() {
		return &ValidationError{Report: rep}
	}
	return nil
}

// Get returns all records with the given key, in chain order.
func Get(f *File, key int64) ([]Record, error) {
	if err := checked(f); err != nil {
		return nil, err
	}
	var out []Record
	for _, p := range chain(f) {
		for _, r := range p.Records {
			if r.Key == key {
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// Scan returns all records with lo <= key <= hi, in chain order.
func Scan(f *File, lo, hi int64) ([]Record, error) {
	if err := checked(f); err != nil {
		return nil, err
	}
	var out []Record
	for _, p := range chain(f) {
		for _, r := range p.Records {
			if r.Key >= lo && r.Key <= hi {
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// chain walks the next-page links from the head. Only called after Check
// passed, so the walk is guaranteed cycle-free and fully linked.
func chain(f *File) []*Page {
	var out []*Page
	for id := f.Head; id != NilPage; {
		p := f.PageByID(id)
		if p == nil {
			break
		}
		out = append(out, p)
		id = p.Header.NextPage
	}
	return out
}
