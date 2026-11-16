// Package block1 implements server-side reassembly of CoAP Block1
// (RFC 7959) transfers with defensive limits against memory-exhaustion
// and content-swap attacks.
//
// The package is transport-agnostic: a CoAP server converts each incoming
// Block1 request into a Message, calls Handle, and maps the returned Code
// onto the wire response. All state is kept in memory and bounded by
// MaxPayloadSize per transfer and MaxTransfersPerPeer per peer.
package block1

import (
	"bytes"
	"fmt"
	"sync"
)

// Code is a CoAP response code (class.detail packed as class<<5|detail).
type Code uint8

const (
	CodeContinue                Code = 0x5f // 2.31 Continue
	CodeChanged                 Code = 0x44 // 2.04 Changed
	CodeBadRequest              Code = 0x80 // 4.00 Bad Request
	CodeRequestEntityIncomplete Code = 0x88 // 4.08 Request Entity Incomplete
	CodeRequestEntityTooLarge   Code = 0x8d // 4.13 Request Entity Too Large
	CodeTooManyRequests         Code = 0x9d // 4.29 Too Many Requests
)

func (c Code) String() string {
	return fmt.Sprintf("%d.%02d", c>>5, c&0x1f)
}

const (
	// MaxPayloadSize is the largest reassembled body a transfer may
	// declare (Size1) or accumulate: 64 KiB.
	MaxPayloadSize = 64 * 1024
	// MaxTransfersPerPeer bounds concurrent incomplete transfers from
	// one peer.
	MaxTransfersPerPeer = 4
	// maxSZX limits block sizes to 16..1024 bytes (SZX 0..6).
	maxSZX = 6
)

// Message carries the Block1-relevant fields of one incoming request.
type Message struct {
	Peer     string // stable peer identity, e.g. remote address
	Resource string // request path identifying the target resource

	Num  uint32 // Block1 NUM
	More bool   // Block1 M bit
	SZX  uint   // Block1 SZX, must be 0..6 (16..1024 bytes)

	Size1         int64  // Size1 option value, negative if absent
	ETag          []byte // ETag option value, nil if absent
	ContentFormat int    // Content-Format option value, negative if absent

	Payload []byte // block payload
}

// Result tells the server how to answer a Block1 request.
type Result struct {
	Code     Code   // response code to send back
	Complete bool   // true when the final block was reassembled
	Payload  []byte // reassembled body, valid only when Complete
}

type transferKey struct {
	peer     string
	resource string
}

type transfer struct {
	szx           uint
	blockSize     int
	etag          []byte
	hasETag       bool
	contentFormat int
	nextNum       uint32
	buf           []byte
}

// Reassembler tracks in-progress Block1 transfers. It is safe for
// concurrent use.
type Reassembler struct {
	mu          sync.Mutex
	maxSize     int
	maxPerPeer  int
	transfers   map[transferKey]*transfer
	peerPending map[string]int
}

// New returns a Reassembler with the package default limits.
func New() *Reassembler {
	return &Reassembler{
		maxSize:     MaxPayloadSize,
		maxPerPeer:  MaxTransfersPerPeer,
		transfers:   make(map[transferKey]*transfer),
		peerPending: make(map[string]int),
	}
}

// Handle processes one Block1 request and returns the response to send.
func (r *Reassembler) Handle(m Message) Result {
	if m.SZX > maxSZX {
		return Result{Code: CodeBadRequest}
	}
	blockSize := 1 << (m.SZX + 4)
	if len(m.Payload) > blockSize {
		return Result{Code: CodeBadRequest}
	}
	// Every block except the last must carry a full block of data;
	// otherwise offsets could not be derived from NUM alone.
	if m.More && len(m.Payload) != blockSize {
		return Result{Code: CodeBadRequest}
	}
	// A declared Size1 above the budget is rejected up front, before
	// any state is allocated.
	if m.Size1 > int64(r.maxSize) {
		return Result{Code: CodeRequestEntityTooLarge}
	}

	key := transferKey{peer: m.Peer, resource: m.Resource}

	r.mu.Lock()
	defer r.mu.Unlock()

	if m.Num == 0 {
		return r.startTransfer(key, m, blockSize)
	}
	return r.continueTransfer(key, m, blockSize)
}

func (r *Reassembler) startTransfer(key transferKey, m Message, blockSize int) Result {
	if _, exists := r.transfers[key]; !exists {
		if r.peerPending[key.peer] >= r.maxPerPeer {
			return Result{Code: CodeTooManyRequests}
		}
		r.peerPending[key.peer]++
	}
	t := &transfer{
		szx:           m.SZX,
		blockSize:     blockSize,
		hasETag:       m.ETag != nil,
		contentFormat: m.ContentFormat,
		nextNum:       1,
		buf:           append([]byte(nil), m.Payload...),
	}
	if t.hasETag {
		t.etag = append([]byte(nil), m.ETag...)
	}
	r.transfers[key] = t
	return r.finishIfDone(key, t, m)
}

func (r *Reassembler) continueTransfer(key transferKey, m Message, blockSize int) Result {
	t, ok := r.transfers[key]
	if !ok {
		// No state for this transfer: the peer either skipped NUM 0 or
		// the transfer was already dropped.
		return Result{Code: CodeRequestEntityIncomplete}
	}
	// Blocks must arrive strictly in order. A NUM gap (including a huge
	// jump meant to inflate the buffer) is rejected without touching
	// the stored state, so the peer may retransmit the missing block.
	if m.Num != t.nextNum {
		return Result{Code: CodeRequestEntityIncomplete}
	}
	// The block size is fixed for the whole transfer.
	if m.SZX != t.szx {
		r.dropTransfer(key)
		return Result{Code: CodeBadRequest}
	}
	// ETag and Content-Format identify the body being uploaded; a
	// mid-transfer change means the peer switched content, so the
	// partially reassembled body is discarded entirely.
	if (m.ETag != nil) != t.hasETag || !bytes.Equal(m.ETag, t.etag) ||
		m.ContentFormat != t.contentFormat {
		r.dropTransfer(key)
		return Result{Code: CodeRequestEntityIncomplete}
	}
	t.buf = append(t.buf, m.Payload...)
	if len(t.buf) > r.maxSize {
		r.dropTransfer(key)
		return Result{Code: CodeRequestEntityTooLarge}
	}
	t.nextNum++
	return r.finishIfDone(key, t, m)
}

func (r *Reassembler) finishIfDone(key transferKey, t *transfer, m Message) Result {
	if m.More {
		return Result{Code: CodeContinue}
	}
	r.dropTransfer(key)
	return Result{Code: CodeChanged, Complete: true, Payload: t.buf}
}

// dropTransfer removes a transfer and releases its per-peer slot.
// Callers must hold r.mu.
func (r *Reassembler) dropTransfer(key transferKey) {
	if _, ok := r.transfers[key]; !ok {
		return
	}
	delete(r.transfers, key)
	r.peerPending[key.peer]--
	if r.peerPending[key.peer] <= 0 {
		delete(r.peerPending, key.peer)
	}
}
