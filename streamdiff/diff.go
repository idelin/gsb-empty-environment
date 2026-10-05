package streamdiff

import (
	"context"
	"fmt"
	"io"
)

// Diff streams the difference between two sorted sources into sink.
//
// left is the "old" side, right the "new" side. Both must yield unique keys
// in strictly ascending order and must have been opened so that their first
// record (if any) has key > start.LastKey.
//
// Backpressure: changes are delivered through the blocking Sink.Emit call;
// the merge never runs ahead of the consumer and never buffers more than
// one record per side. Cancellation: ctx is checked between steps and
// propagated to Source.Next and Sink.Emit to abort promptly.
//
// The returned cursor is the durable resume position: it advances past a
// change only after Sink.Emit has accepted it, so resuming from it never
// re-emits a delivered change. If the returned error is nil the diff is
// complete and the cursor sits past the greatest key of both streams.
func Diff(ctx context.Context, left, right Source, start Cursor, sink Sink) (Cursor, error) {
	cur := start
	if cur.LeftVersion == "" && cur.RightVersion == "" {
		cur.LeftVersion, cur.RightVersion = left.Version(), right.Version()
	} else if cur.LeftVersion != left.Version() || cur.RightVersion != right.Version() {
		return start, fmt.Errorf("streamdiff: cursor bound to versions (%s,%s), got (%s,%s)",
			cur.LeftVersion, cur.RightVersion, left.Version(), right.Version())
	}

	var l, r Record
	lOK, rOK := false, false
	needL, needR := true, true
	lastL, lastR := cur.LastKey, cur.LastKey

	for {
		if err := ctx.Err(); err != nil {
			return cur, err
		}
		if needL {
			rec, err := left.Next(ctx)
			switch {
			case err == io.EOF:
				lOK = false
			case err != nil:
				return cur, err
			default:
				if rec.Key <= lastL {
					return cur, fmt.Errorf("streamdiff: left source out of order: %q after %q", rec.Key, lastL)
				}
				l, lOK, lastL = rec, true, rec.Key
			}
			needL = false
		}
		if needR {
			rec, err := right.Next(ctx)
			switch {
			case err == io.EOF:
				rOK = false
			case err != nil:
				return cur, err
			default:
				if rec.Key <= lastR {
					return cur, fmt.Errorf("streamdiff: right source out of order: %q after %q", rec.Key, lastR)
				}
				r, rOK, lastR = rec, true, rec.Key
			}
			needR = false
		}
		if !lOK && !rOK {
			return cur, nil
		}

		var c Change
		switch {
		case !rOK || (lOK && l.Key < r.Key):
			c = Change{Kind: Removed, Key: l.Key, Old: l.Value}
			needL = true
		case !lOK || r.Key < l.Key:
			c = Change{Kind: Added, Key: r.Key, New: r.Value}
			needR = true
		default:
			eq, err := equalValues(l.Value, r.Value)
			if err != nil {
				return cur, err
			}
			needL, needR = true, true
			if eq {
				cur.LastKey = l.Key
				continue
			}
			c = Change{Kind: Modified, Key: l.Key, Old: l.Value, New: r.Value}
		}

		if err := sink.Emit(ctx, c); err != nil {
			return cur, err
		}
		cur.LastKey = c.Key
	}
}
