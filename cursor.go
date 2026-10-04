package foldindex

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidCursor is returned when a cursor token is malformed.
var ErrInvalidCursor = errors.New("foldindex: malformed cursor")

// ErrStaleCursor is returned when a cursor was issued by an index built
// with a different table version or different content and must not be
// mixed with this index.
var ErrStaleCursor = errors.New("foldindex: cursor belongs to a different index")

const cursorMagic = "fx1"

// Page is one page of search results. NextCursor is empty when the page
// is the last one.
type Page struct {
	Hits       []Hit
	NextCursor string
}

// SearchPage behaves like Search but returns at most limit hits,
// resuming from cursor (empty string starts from the beginning). The
// cursor binds the table version and index fingerprint, so cursors
// issued before a table re-versioning are rejected with ErrStaleCursor.
func (ix *Index) SearchPage(query string, limit int, cursor string) (Page, error) {
	if limit <= 0 {
		limit = 20
	}
	offset, err := ix.decodeCursor(cursor)
	if err != nil {
		return Page{}, err
	}
	hits := ix.Search(query)
	if offset > len(hits) {
		offset = len(hits)
	}
	end := offset + limit
	if end > len(hits) {
		end = len(hits)
	}
	page := Page{Hits: hits[offset:end]}
	if end < len(hits) {
		page.NextCursor = ix.encodeCursor(end)
	}
	return page, nil
}

func (ix *Index) encodeCursor(offset int) string {
	raw := fmt.Sprintf("%s|%s|%d|%d", cursorMagic, ix.table.version, ix.fingerprint, offset)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func (ix *Index) decodeCursor(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, ErrInvalidCursor
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 || parts[0] != cursorMagic {
		return 0, ErrInvalidCursor
	}
	offset, err := strconv.Atoi(parts[3])
	if err != nil || offset < 0 {
		return 0, ErrInvalidCursor
	}
	if parts[1] != ix.table.version || parts[2] != strconv.FormatUint(ix.fingerprint, 10) {
		return 0, ErrStaleCursor
	}
	return offset, nil
}
