package netsec

import (
	"context"
	"math"
)

type chunk struct {
	iova       uint64
	iovaPage   uint64
	physOffset uint64
	length     uint64
	entry      *mapping
}

// Transfer validates an untrusted device request and invokes access once per
// page-bounded chunk.
//
// The complete scatter-gather list, all translations, permissions, page
// ownership, and arithmetic are checked before the first callback. Leases are
// acquired for every chunk under one lock acquisition, so a concurrent Remap
// cannot produce a torn transfer. Unmap removes mappings immediately but waits
// for these leases before returning.
func (m *IOMMU) Transfer(ctx context.Context, req TransferRequest, access AccessFunc) (TransferResult, error) {
	var result TransferResult
	if access == nil {
		return result, ErrNilCallback
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !req.Access.valid() {
		return result, ErrInvalidAccess
	}

	ranges, err := requestRanges(req)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	chunks, err := m.lease(req.Device, ranges, req.Access)
	if err != nil {
		return result, err
	}
	defer m.release(chunks)

	for _, c := range chunks {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		op := AccessOp{
			Device:     req.Device,
			Domain:     c.entry.domain,
			Generation: c.entry.generation,
			IOVA:       c.iova,
			PhysPage:   c.entry.physPage,
			PhysOffset: c.physOffset,
			Length:     c.length,
			Access:     req.Access,
		}
		if err := access(op); err != nil {
			return result, err
		}
		result.Chunks++
		result.Bytes += c.length
	}
	return result, nil
}

func requestRanges(req TransferRequest) ([]Segment, error) {
	if req.Length == 0 {
		return nil, ErrInvalidLength
	}
	if req.Length > math.MaxUint64-req.IOVA {
		return nil, ErrOverflow
	}
	windowEnd := req.IOVA + req.Length
	if len(req.SG) == 0 {
		return []Segment{{IOVA: req.IOVA, Length: req.Length}}, nil
	}

	ranges := make([]Segment, 0, len(req.SG))
	var total uint64
	for _, seg := range req.SG {
		if seg.Length == 0 {
			return nil, ErrInvalidLength
		}
		if seg.Length > math.MaxUint64-seg.IOVA {
			return nil, ErrOverflow
		}
		end := seg.IOVA + seg.Length
		if seg.IOVA < req.IOVA || end > windowEnd {
			return nil, ErrInvalidIOVA
		}
		if seg.Length > math.MaxUint64-total {
			return nil, ErrOverflow
		}
		total += seg.Length
		ranges = append(ranges, seg)
	}
	if total != req.Length {
		return nil, ErrInvalidLength
	}
	return ranges, nil
}

func (m *IOMMU) lease(device DeviceID, ranges []Segment, want AccPerm) ([]*chunk, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	dev, err := m.deviceLocked(device)
	if err != nil {
		return nil, err
	}

	var chunks []*chunk
	for _, seg := range ranges {
		remaining := seg.Length
		iova := seg.IOVA
		for remaining != 0 {
			pageOffset := iova % PageSize
			n := PageSize - pageOffset
			if n > remaining {
				n = remaining
			}
			iovaPage := iova / PageSize
			entry, ok := dev.mappings[iovaPage]
			if !ok || !entry.active {
				return nil, ErrNotMapped
			}
			if entry.domain != dev.domain || entry.tenant != dev.tenant || entry.owner != dev {
				return nil, ErrMixedDomain
			}
			if owner, ok := m.pageOwners[entry.physPage]; !ok {
				return nil, ErrPageUnowned
			} else if owner != dev.tenant {
				return nil, ErrMixedDomain
			}
			if !entry.access.Allows(want) {
				return nil, ErrPermission
			}
			chunks = append(chunks, &chunk{
				iova:       iova,
				iovaPage:   iovaPage,
				physOffset: pageOffset,
				length:     n,
				entry:      entry,
			})
			iova += n
			remaining -= n
		}
	}

	for _, c := range chunks {
		c.entry.refs++
	}
	return chunks, nil
}

func (m *IOMMU) release(chunks []*chunk) {
	m.mu.Lock()
	for _, c := range chunks {
		c.entry.refs--
		if c.entry.refs == 0 && !c.entry.active {
			delete(c.entry.owner.retired, c.entry)
		}
	}
	m.cond.Broadcast()
	m.mu.Unlock()
}
