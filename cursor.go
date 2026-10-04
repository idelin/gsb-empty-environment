package streamdiff

import (
	"context"
	"fmt"
)

// Resume prepares a pair of freshly opened sources to continue a diff from
// cur, typically after a *Failure. Both sources must be positioned at the
// start of their snapshot and must match the versions recorded in cur;
// otherwise an error is returned and no resumption takes place.
//
// The returned sources yield records strictly after cur.AfterKey, so
// re-running Diff on them never re-emits an event already delivered.
func Resume(cur Cursor, oldSrc, newSrc Source) (Source, Source, error) {
	if v := oldSrc.Version(); v != cur.OldVersion {
		return nil, nil, fmt.Errorf("streamdiff: old source version %d does not match cursor version %d", v, cur.OldVersion)
	}
	if v := newSrc.Version(); v != cur.NewVersion {
		return nil, nil, fmt.Errorf("streamdiff: new source version %d does not match cursor version %d", v, cur.NewVersion)
	}
	return afterKey(oldSrc, cur.AfterKey), afterKey(newSrc, cur.AfterKey), nil
}

// afterKey positions s strictly after key, using a native Seek when the
// source supports it and a filtering wrapper otherwise.
func afterKey(s Source, key string) Source {
	if key == "" {
		return s
	}
	if sk, ok := s.(SeekSource); ok {
		if err := sk.Seek(key); err == nil {
			return sk
		}
	}
	return &skipSource{src: s, afterKey: key}
}

// skipSource filters out records at or before afterKey. It relies on the
// source's ordering guarantee and stops skipping at the first larger key.
type skipSource struct {
	src      Source
	afterKey string
	skipping bool
}

func (s *skipSource) Version() uint64 { return s.src.Version() }

func (s *skipSource) Next(ctx context.Context) (Record, error) {
	if !s.skipping {
		s.skipping = true
		for {
			r, err := s.src.Next(ctx)
			if err != nil {
				return r, err
			}
			if r.Key > s.afterKey {
				return r, nil
			}
		}
	}
	return s.src.Next(ctx)
}
