package netsec

import "errors"

// Shard is one non-overlapping work partition of an authorization.
//
// Region is a half-open integer box whose volume never exceeds the
// MaxVolume requested at sharding time. Retrying a shard reuses the same
// Region, so a retry can never touch points outside the original shard.
type Shard struct {
	Region Box
}

// Split splits accessible (the authorization region after denied areas have
// been removed, typically produced by Subtract) into disjoint shards whose
// individual volumes are at most maxVolume integer points.
//
// The shards are pairwise disjoint, every shard lies inside one input box,
// and the union of all shards equals the union of the input boxes exactly:
// no holes and no duplicated coverage. maxVolume must be positive; every
// input box must be valid and non-empty.
func Split(accessible []Box, maxVolume int64) ([]Shard, error) {
	if maxVolume <= 0 {
		return nil, errors.New("netsec: maxVolume must be positive")
	}
	var shards []Shard
	for _, b := range accessible {
		if err := b.validate(); err != nil {
			return nil, err
		}
		if b.Empty() {
			continue
		}
		vol, err := b.Volume()
		if err != nil {
			return nil, err
		}
		shards = append(shards, splitBox(b, vol, maxVolume)...)
	}
	return shards, nil
}

// splitBox recursively bisects b until every piece has volume <= maxVolume.
// The longest dimension is always split, so each cut is balanced and the
// recursion depth stays within the total bit length of the volume.
func splitBox(b Box, vol, maxVolume int64) []Shard {
	if vol <= maxVolume {
		return []Shard{{Region: b}}
	}

	dim := -1
	var longest int64
	for i := range b.Lo {
		length := b.Hi[i] - b.Lo[i]
		if length > longest {
			longest = length
			dim = i
		}
	}
	// vol > maxVolume >= 1 implies some dimension has length >= 2.

	leftLen := longest / 2
	mid := b.Lo[dim] + leftLen

	left := Box{Lo: append([]int64(nil), b.Lo...), Hi: append([]int64(nil), b.Hi...)}
	left.Hi[dim] = mid
	right := Box{Lo: append([]int64(nil), b.Lo...), Hi: append([]int64(nil), b.Hi...)}
	right.Lo[dim] = mid

	leftVol := leftLen
	rightVol := longest - leftLen
	for i := range b.Lo {
		if i == dim {
			continue
		}
		length := b.Hi[i] - b.Lo[i]
		leftVol *= length
		rightVol *= length
	}

	var out []Shard
	out = append(out, splitBox(left, leftVol, maxVolume)...)
	out = append(out, splitBox(right, rightVol, maxVolume)...)
	return out
}
