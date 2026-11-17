package netsec

import (
	"errors"
	"fmt"
	"math/bits"
)

// Segment is one entry of a device-controlled scatter-gather list.
type Segment struct {
	IOVA IOVA
	Len  uint64
}

// MemOp is one page-sized (or smaller, for segment edges) physical memory
// operation produced by validating and splitting a scatter-gather list.
type MemOp struct {
	Phys   PhysAddr
	Len    uint64
	Write  bool
	Tenant TenantID
}

// Lease is an atomic take of every page referenced by a validated request.
// The generation captured in the lease stays usable even after the domain
// remaps or revokes; releasing the lease returns those references.
type Lease struct {
	domain *Domain
	gen    uint64
	pages  int
	ops    []MemOp
	onOnce bool
}

// Generation returns the domain generation the lease was taken against.
func (l *Lease) Generation() uint64 { return l.gen }

// Current reports whether the lease's generation still matches the domain's
// current generation. A remap, unmap or revoke makes the report false with
// ErrStale. The lease itself stays usable regardless: an in-flight transfer
// captured by the lease may complete until Release is called.
func (l *Lease) Current() (bool, error) {
	d := l.domain
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.gen != l.gen {
		return false, fmt.Errorf("%w: lease gen %d, domain gen %d", ErrStale, l.gen, d.gen)
	}
	return true, nil
}

// Pages returns the number of page references held by the lease. Repeated
// references to the same page count each time they appear.
func (l *Lease) Pages() int { return l.pages }

// Operations returns the page-split physical operations captured by the
// lease. The returned slice must not be mutated.
func (l *Lease) Operations() []MemOp { return l.ops }

// Release returns the lease. It is safe to call more than once; subsequent
// calls are no-ops.
func (l *Lease) Release() {
	d := l.domain
	d.mu.Lock()
	if l.onOnce {
		d.mu.Unlock()
		return
	}
	l.onOnce = true
	d.leases--
	if d.leases == 0 {
		d.cond.Broadcast()
	}
	d.mu.Unlock()
}

// MemCallback is a host-side memory access callback. It is invoked outside all
// package locks, once per page-split operation, only after the entire
// scatter-gather list has been validated and the lease taken.
type MemCallback func(op MemOp) error

// TransferResult summarizes a completed transfer.
type TransferResult struct {
	// Generation is the domain generation the transfer ran against.
	Generation uint64
	// Operations is the number of page-split memory callbacks performed.
	Operations int
	// Bytes is the total number of bytes covered by the request.
	Bytes uint64
}

// Acquire validates the complete device-controlled scatter-gather list for
// device and, if every page is mapped, permitted, owned by a single tenant
// and free of overflow, atomically takes a lease over all touched pages.
//
// No memory callback can run on the failure path: either the whole list is
// valid and a lease is returned, or an error is returned with no side effect.
func (io *IOMMU) Acquire(device DeviceID, list []Segment, write bool) (*Lease, error) {
	d, err := io.domain(device)
	if err != nil {
		return nil, err
	}

	// Take the domain lock first and the host read lock second. The only
	// host writer (Assign) never takes a domain lock, so no lock-order cycle
	// exists; the read lock additionally prevents ownership changes while a
	// multi-page list is being validated against the ownership table.
	d.mu.Lock()
	defer d.mu.Unlock()
	d.host.mu.RLock()
	defer d.host.mu.RUnlock()

	ops, pages, _, err := d.resolveLocked(list, write)
	if err != nil {
		return nil, err
	}

	d.leases++
	return &Lease{
		domain: d,
		gen:    d.gen,
		pages:  pages,
		ops:    ops,
	}, nil
}

// Transfer validates and performs a DMA request. On success every page-split
// operation is passed to cb in list order and the lease is released once all
// callbacks complete. If cb fails, remaining operations are skipped, the
// error is wrapped with ErrCallback and the lease is still released.
func (io *IOMMU) Transfer(device DeviceID, list []Segment, write bool, cb MemCallback) (TransferResult, error) {
	if cb == nil {
		return TransferResult{}, fmt.Errorf("%w: nil memory callback", ErrBadRequest)
	}
	lease, err := io.Acquire(device, list, write)
	if err != nil {
		return TransferResult{}, err
	}
	defer lease.Release()

	var performed int
	for _, op := range lease.ops {
		if err := cb(op); err != nil {
			return TransferResult{
				Generation: lease.gen,
				Operations: performed,
				Bytes:      bytesOf(lease.ops),
			}, errors.Join(ErrCallback, err)
		}
		performed++
	}
	return TransferResult{
		Generation: lease.gen,
		Operations: performed,
		Bytes:      bytesOf(lease.ops),
	}, nil
}

func bytesOf(ops []MemOp) uint64 {
	var total uint64
	for _, op := range ops {
		total += op.Len
	}
	return total
}

// resolveLocked validates an entire scatter-gather list against the current
// page table while the caller holds d.mu and the host read lock. It performs
// all checks before returning, so a failure leaves no lease and no callback.
func (d *Domain) resolveLocked(list []Segment, write bool) (ops []MemOp, pages int, total uint64, err error) {
	if len(list) == 0 {
		return nil, 0, 0, ErrEmptyList
	}

	need := PermRead
	if write {
		need = PermWrite
	}

	var tenant TenantID
	haveTenant := false
	ops = make([]MemOp, 0, len(list))

	for si, seg := range list {
		if seg.Len == 0 {
			return nil, 0, 0, fmt.Errorf("%w: segment %d has zero length", ErrEmptyList, si)
		}
		// last = iova + len - 1 must fit in uint64.
		end, carry := bits.Add64(uint64(seg.IOVA), seg.Len, 0)
		if carry == 1 || end == 0 {
			return nil, 0, 0, fmt.Errorf("%w: segment %d iova %#x len %d",
				ErrOverflow, si, uint64(seg.IOVA), seg.Len)
		}
		if total+seg.Len < total || total+seg.Len > d.maxBytes {
			return nil, 0, 0, fmt.Errorf("%w: segment %d pushes total to %d (limit %d)",
				ErrTooLarge, si, total+seg.Len, d.maxBytes)
		}
		total += seg.Len

		firstIFN := uint64(seg.IOVA) / PageSize
		lastIFN := (end - 1) / PageSize

		cur := uint64(seg.IOVA)
		remaining := seg.Len
		segPages := uint64(0)
		for remaining != 0 {
			ifn := cur / PageSize
			entry, ok := d.tab[ifn]
			if !ok {
				return nil, 0, 0, fmt.Errorf("%w: iova frame %#x (segment %d)", ErrUnmapped, ifn, si)
			}
			if !entry.perm.Contains(need) {
				return nil, 0, 0, fmt.Errorf("%w: iova frame %#x has %s, need %s (segment %d)",
					ErrPermission, ifn, entry.perm, need, si)
			}
			owner, owned := d.host.OwnerOf(PhysAddr(entry.pfn * PageSize))
			if !owned {
				return nil, 0, 0, fmt.Errorf("%w: frame %#x has no host owner (segment %d)",
					ErrOwnerMismatch, entry.pfn, si)
			}
			if owner != entry.tenant {
				return nil, 0, 0, fmt.Errorf("%w: frame %#x pte tenant %d but host tenant %d (segment %d)",
					ErrOwnerMismatch, entry.pfn, entry.tenant, owner, si)
			}
			if !haveTenant {
				tenant = entry.tenant
				haveTenant = true
			} else if entry.tenant != tenant {
				return nil, 0, 0, fmt.Errorf("%w: tenant %d vs %d (segment %d)",
					ErrMixedTenant, tenant, entry.tenant, si)
			}

			off := cur & pageMask
			chunk := PageSize - off
			if chunk > remaining {
				chunk = remaining
			}
			ops = append(ops, MemOp{
				Phys:   PhysAddr(entry.pfn*PageSize + off),
				Len:    chunk,
				Write:  write,
				Tenant: entry.tenant,
			})
			pages++
			segPages++
			remaining -= chunk
			cur += chunk
		}

		if touched := lastIFN - firstIFN + 1; segPages != touched {
			return nil, 0, 0, fmt.Errorf("%w: internal page split bookkeeping", ErrOverflow)
		}
	}

	return ops, pages, total, nil
}
