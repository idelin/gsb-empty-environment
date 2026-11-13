package block1

import (
	"sync"
	"time"
)

type transfer struct {
	peer             string
	key              transferKey
	num              uint32 // next expected block number
	szx              SZX
	blockSize        int
	size1            uint32
	hasSize1         bool
	etag             []byte
	hasETag          bool
	contentFormat    uint16
	hasContentFormat bool
	buf              []byte
	lastSeen         time.Time
}

type transferKey struct {
	peer  string
	token string
}

// Reassembler tracks all Block1 transfers. The zero value is not usable;
// create one with NewReassembler. Methods are safe for concurrent use.
type Reassembler struct {
	mu               sync.Mutex
	maxBody          int
	maxIncomplete    int
	transfers        map[transferKey]*transfer
	incompleteByPeer map[string]int
}

// NewReassembler returns a Reassembler with the mandated limits: 64 KiB per
// body and at most four incomplete transfers per peer.
func NewReassembler() *Reassembler {
	return NewReassemblerWithLimits(MaxBodySize, MaxIncompletePerPeer)
}

// NewReassemblerWithLimits builds a Reassembler with custom limits, mainly
// useful in tests.
func NewReassemblerWithLimits(maxBodyBytes, maxIncompletePerPeer int) *Reassembler {
	if maxBodyBytes <= 0 {
		maxBodyBytes = MaxBodySize
	}
	if maxIncompletePerPeer <= 0 {
		maxIncompletePerPeer = MaxIncompletePerPeer
	}
	return &Reassembler{
		maxBody:          maxBodyBytes,
		maxIncomplete:    maxIncompletePerPeer,
		transfers:        make(map[transferKey]*transfer),
		incompleteByPeer: make(map[string]int),
	}
}

// IncompleteCount reports how many unfinished transfers a peer currently owns.
func (r *Reassembler) IncompleteCount(peer string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.incompleteByPeer[peer]
}

// Prune drops incomplete transfers that have seen no fragment for longer than
// idle and returns the number removed. Call it periodically to release state
// abandoned by peers.
func (r *Reassembler) Prune(idle time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	removed := 0
	for key, tr := range r.transfers {
		if now.Sub(tr.lastSeen) > idle {
			delete(r.transfers, key)
			r.incompleteByPeer[tr.peer]--
			if r.incompleteByPeer[tr.peer] <= 0 {
				delete(r.incompleteByPeer, tr.peer)
			}
			removed++
		}
	}
	return removed
}

// Process validates and stores one Block1 fragment, returning the response the
// server should send. Rejected fragments never allocate body storage beyond
// the fragment already received from the network.
func (r *Reassembler) Process(f Fragment) Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	blockSize, err := f.SZX.Size()
	if err != nil {
		return reject(CodeBadRequest, err)
	}
	if len(f.Payload) > blockSize {
		return reject(CodeBadRequest, ErrPayloadExceedsBlock)
	}

	key := transferKey{peer: f.Peer, token: string(f.Token)}
	tr, exists := r.transfers[key]
	if !exists {
		// A transfer starts at NUM 0. A large NUM therefore cannot force the
		// server to pre-allocate or reserve anything.
		if f.NUM != 0 {
			return reject(CodeEntityIncomplete, ErrOutOfOrder)
		}
		if f.HasSize1 && f.Size1 > uint32(r.maxBody) {
			return reject(CodeRequestEntityTooLarge, ErrSize1TooLarge)
		}
		if r.incompleteByPeer[f.Peer] >= r.maxIncomplete {
			return reject(CodeServiceUnavailable, ErrTooManyTransfers)
		}
		tr = &transfer{
			peer:             f.Peer,
			key:              key,
			num:              0,
			szx:              f.SZX,
			blockSize:        blockSize,
			size1:            f.Size1,
			hasSize1:         f.HasSize1,
			etag:             append([]byte(nil), f.ETag...),
			hasETag:          f.HasETag,
			contentFormat:    f.ContentFormat,
			hasContentFormat: f.HasContentFormat,
			lastSeen:         time.Now(),
		}
		r.transfers[key] = tr
		r.incompleteByPeer[f.Peer]++
	} else {
		// Representation and transfer parameters are immutable after the
		// first block; any change invalidates the whole transfer.
		if f.SZX != tr.szx {
			r.discard(tr)
			return reject(CodeBadRequest, ErrSZXChanged)
		}
		if f.HasETag != tr.hasETag || (f.HasETag && string(f.ETag) != string(tr.etag)) {
			r.discard(tr)
			return reject(CodeConflict, ErrETagChanged)
		}
		if f.HasContentFormat != tr.hasContentFormat ||
			(f.HasContentFormat && f.ContentFormat != tr.contentFormat) {
			r.discard(tr)
			return reject(CodeConflict, ErrContentFormatChanged)
		}
		if f.HasSize1 != tr.hasSize1 || (f.HasSize1 && f.Size1 != tr.size1) {
			r.discard(tr)
			return reject(CodeBadRequest, ErrSize1Changed)
		}
		if f.HasSize1 && f.Size1 > uint32(r.maxBody) {
			r.discard(tr)
			return reject(CodeRequestEntityTooLarge, ErrSize1TooLarge)
		}
		if f.NUM != tr.num {
			// Includes duplicate/retransmitted NUMs: strict sequence only.
			return Result{Status: StatusRejected, Code: CodeEntityIncomplete, WantNUM: tr.num, Err: ErrOutOfOrder}
		}
	}

	// A NUM cannot legitimately lie beyond the declared body: this also keeps
	// the expected-offset arithmetic within bounded ints.
	offset := int(f.NUM) * blockSize
	if tr.hasSize1 && offset >= int(tr.size1) {
		r.discard(tr)
		return reject(CodeBadRequest, ErrExceedsSize1)
	}
	if offset+len(f.Payload) > r.maxBody {
		r.discard(tr)
		return reject(CodeRequestEntityTooLarge, ErrSize1TooLarge)
	}
	if tr.hasSize1 && offset+len(f.Payload) > int(tr.size1) {
		r.discard(tr)
		return reject(CodeBadRequest, ErrExceedsSize1)
	}
	if f.More && len(f.Payload) != blockSize {
		r.discard(tr)
		return reject(CodeBadRequest, ErrShortBlock)
	}

	tr.buf = append(tr.buf, f.Payload...)
	tr.num = f.NUM + 1
	tr.lastSeen = time.Now()

	if f.More {
		return Result{Status: StatusContinue, Code: CodeContinue, WantNUM: tr.num}
	}

	// Final block: the reassembled length must match the declared Size1.
	if tr.hasSize1 && uint32(len(tr.buf)) != tr.size1 {
		r.discard(tr)
		return reject(CodeEntityIncomplete, ErrSizeMismatch)
	}
	r.incompleteByPeer[tr.peer]--
	if r.incompleteByPeer[tr.peer] <= 0 {
		delete(r.incompleteByPeer, tr.peer)
	}
	body := tr.buf
	delete(r.transfers, tr.key)
	return Result{Status: StatusComplete, Code: CodeChanged, Body: body}
}

func (r *Reassembler) discard(tr *transfer) {
	delete(r.transfers, tr.key)
	r.incompleteByPeer[tr.peer]--
	if r.incompleteByPeer[tr.peer] <= 0 {
		delete(r.incompleteByPeer, tr.peer)
	}
}

func reject(code Code, err error) Result {
	return Result{Status: StatusRejected, Code: code, Err: err}
}
