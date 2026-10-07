package snapshotscan

import "errors"

// ErrInvalidLimit is returned for non-positive page sizes.
var ErrInvalidLimit = errors.New("snapshotscan: limit must be positive")

// Scanner delivers snapshot-pinned, permission-filtered pages.
//
// Invariants:
//   - Data is read from the snapshot pinned when the scan started.
//   - Permission is evaluated against the latest model state at the
//     moment each item is delivered.
//   - An item revoked before its delivery opportunity is skipped and
//     never leaks; an item already delivered is never retracted.
//   - The cursor carries (snapshot, last scanned key), never the last
//     delivered key.
type Scanner struct {
	store     *Store
	perms     *PermissionModel
	batchSize int
}

// NewScanner builds a Scanner fetching snapshot rows in batches of
// batchSize while assembling each delivery page.
func NewScanner(store *Store, perms *PermissionModel, batchSize int) *Scanner {
	if batchSize < 1 {
		batchSize = 1
	}
	return &Scanner{store: store, perms: perms, batchSize: batchSize}
}

// Page is one delivery unit.
type Page struct {
	Items []Item
	// NextCursor resumes the scan. It is always non-empty, including on
	// the terminal page, so callers can observe the final scan position.
	NextCursor string
	// HasMore is false once the snapshot is fully scanned.
	HasMore bool
}

// Scan delivers the next page for subject. An empty cursorToken starts
// a new scan and pins the current data version.
func (s *Scanner) Scan(subject, cursorToken string, limit int) (Page, error) {
	if limit <= 0 {
		return Page{}, ErrInvalidLimit
	}
	cur := Cursor{}
	if cursorToken == "" {
		cur.Snapshot = s.store.Pin()
	} else {
		decoded, err := DecodeCursor(cursorToken)
		if err != nil {
			return Page{}, err
		}
		cur = decoded
	}

	delivered := make([]Item, 0, limit)
	scanned := cur.LastScanned
	for len(delivered) < limit {
		batch, err := s.store.ScanAt(cur.Snapshot, scanned, s.batchSize)
		if err != nil {
			return Page{}, err
		}
		if len(batch) == 0 {
			break // snapshot exhausted
		}
		for _, item := range batch {
			scanned = item.Key // advance scan position for every scanned item
			// Dynamic check: latest permission version at delivery time.
			if s.perms.Allowed(subject, item.Key) {
				delivered = append(delivered, item)
				if len(delivered) == limit {
					break
				}
			}
		}
	}

	hasMore, err := s.store.HasKeysAfter(cur.Snapshot, scanned)
	if err != nil {
		return Page{}, err
	}
	return Page{
		Items:      delivered,
		NextCursor: EncodeCursor(Cursor{Snapshot: cur.Snapshot, LastScanned: scanned}),
		HasMore:    hasMore,
	}, nil
}
