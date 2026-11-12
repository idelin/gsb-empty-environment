package netsec

import (
	"context"
	"errors"
	"fmt"
)

// Open validates the graph reachable from rootID and returns a ready Reader.
// Validation proves acyclicity and structural well-formedness before a single
// payload byte is decoded; the output and expansion budgets are enforced
// afterwards while reading.
func Open(ctx context.Context, store BlockStore, rootID int64, lim Limits) (*Reader, error) {
	if store == nil {
		return nil, errors.New("netsec: nil BlockStore")
	}
	if lim.MaxOutputBytes <= 0 {
		lim.MaxOutputBytes = defaultMaxOutputBytes
	}
	if lim.MaxExpansions <= 0 {
		lim.MaxExpansions = defaultMaxExpansions
	}
	if lim.MaxDepth <= 0 {
		lim.MaxDepth = defaultMaxDepth
	}
	if lim.MaxBlocks <= 0 {
		lim.MaxBlocks = defaultMaxBlocks
	}
	if lim.MaxCacheBytes <= 0 {
		lim.MaxCacheBytes = defaultMaxCacheBytes
	}

	r := &Reader{
		store:    store,
		lim:      lim,
		ctx:      ctx,
		cache:    make(map[int64][]byte),
		inflight: make(map[int64]bool),
	}

	// color: 0 unseen, 1 on the current DFS path, 2 fully explored. proven
	// caches the logical size of each validated block for post-order checks.
	color := make(map[int64]int8)
	proven := make(map[int64]int64)

	var validate func(id int64, depth int) (int64, error)
	validate = func(id int64, depth int) (int64, error) {
		if depth > lim.MaxDepth {
			return 0, ErrDepthLimit
		}
		switch color[id] {
		case 1:
			return 0, ErrCycle
		case 2:
			return proven[id], nil
		}
		if len(color) >= lim.MaxBlocks {
			return 0, ErrBlockLimit
		}
		color[id] = 1

		blk, err := store.Get(ctx, id)
		if err != nil {
			return 0, err
		}
		if blk == nil || blk.ID != id {
			return 0, fmt.Errorf("%w: block %d", ErrUnknownBlock, id)
		}
		if blk.Declared < 0 {
			return 0, fmt.Errorf("%w: block %d negative size", ErrBadDecl, id)
		}

		var implied int64
		for _, seg := range blk.Segments {
			switch seg.kind {
			case segLiteral:
				implied, err = addCapped(implied, int64(len(seg.data)))
				if err != nil {
					return 0, fmt.Errorf("%w: block %d: %v", ErrBadDecl, id, err)
				}
			case segReference:
				var refSize int64
				refSize, err = validate(seg.ref, depth+1)
				if err != nil {
					return 0, err
				}
				implied, err = addCapped(implied, refSize)
				if err != nil {
					return 0, fmt.Errorf("%w: block %d: %v", ErrBadDecl, id, err)
				}
			default:
				return 0, fmt.Errorf("%w: block %d unknown segment kind", ErrBadDecl, id)
			}
		}

		if blk.Declared != implied {
			return 0, fmt.Errorf("%w: block %d declares %d, produces %d",
				ErrOverlongDecl, id, blk.Declared, implied)
		}
		if blk.Declared > lim.MaxCacheBytes {
			return 0, fmt.Errorf("%w: block %d needs %d bytes, budget %d",
				ErrCacheLimit, id, blk.Declared, lim.MaxCacheBytes)
		}

		color[id] = 2
		proven[id] = implied
		return implied, nil
	}

	if _, err := validate(rootID, 1); err != nil {
		return nil, err
	}
	r.sizes = proven

	rootBlock, err := store.Get(ctx, rootID)
	if err != nil {
		return nil, err
	}
	if rootBlock == nil || rootBlock.ID != rootID {
		return nil, fmt.Errorf("%w: block %d", ErrUnknownBlock, rootID)
	}
	r.stack = []*frame{{block: rootBlock, seg: -1, needAdvance: true}}
	return r, nil
}
