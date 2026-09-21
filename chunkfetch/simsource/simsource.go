// Package simsource is an in-memory random-access Source for demos and tests.
// It can inject scripted transport faults (corrupt frames, truncated frames,
// oversize length fields), split reads into arbitrary small fragments, and
// change the served object version.
package simsource

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"sync"

	"chunkfetch"
)

// FaultKind selects which fault a Source injects for a given chunk.
type FaultKind int

const (
	// FaultCorrupt flips a payload byte, so the wire checksum (or chunk
	// digest) fails -> bad data.
	FaultCorrupt FaultKind = iota
	// FaultTruncated cuts the frame before its end -> missing data.
	FaultTruncated
	// FaultOversize lies about the payload length so it exceeds the
	// allocation quota.
	FaultOversize
)

// Fault is a scripted fault.
type Fault struct {
	Chunk int
	Kind  FaultKind
	Times int // how many attempts it happens before clearing (0 = forever)
}

type Source struct {
	mu sync.Mutex

	manifest *chunkfetch.Manifest
	versions map[string][]byte

	rng         *rand.Rand
	maxFragment int
	faults      map[int]*Fault
	readCalls   map[int]int

	// flipOnRead changes the served version after this many ReadAt calls
	// (0 = disabled). After the flip, reads for the old version fail with
	// VersionMismatchError.
	flipAfter int
	reads     int
	pending   *pendingFlip
}

// New builds a source serving version over data, hashing with chunkSize.
func New(version string, data []byte, chunkSize int) *Source {
	return &Source{
		manifest:    chunkfetch.BuildManifest(version, data, chunkSize),
		versions:    map[string][]byte{version: append([]byte(nil), data...)},
		rng:         rand.New(rand.NewSource(1)),
		maxFragment: 1, // force arbitrary byte-level chunking by default
		faults:      map[int]*Fault{},
		readCalls:   map[int]int{},
	}
}

// SetMaxFragment changes the maximum bytes one Read call returns.
func (s *Source) SetMaxFragment(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxFragment = n
}

// AddFault schedules a fault for chunk c.
func (s *Source) AddFault(c int, kind FaultKind, times int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults[c] = &Fault{Chunk: c, Kind: kind, Times: times}
}

// ReadCalls reports how many ReadAt calls chunk c received.
func (s *Source) ReadCalls(c int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readCalls[c]
}

// Manifest returns the currently served manifest.
func (s *Source) Manifest(_ context.Context) (*chunkfetch.Manifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *s.manifest
	chunks := make([][]byte, len(s.manifest.ChunkSHA256))
	for i, c := range s.manifest.ChunkSHA256 {
		chunks[i] = append([]byte(nil), c...)
	}
	cp.ChunkSHA256 = chunks
	cp.WholeSHA256 = append([]byte(nil), s.manifest.WholeSHA256...)
	return &cp, nil
}

// Replace swaps the served object to newData under a new version, as a server
// would when the object changes. Old-version reads then report a mismatch.
func (s *Source) Replace(version string, newData []byte, chunkSize int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions[version] = append([]byte(nil), newData...)
	s.manifest = chunkfetch.BuildManifest(version, newData, chunkSize)
}

// FlipAfter schedules Replace on the n-th ReadAt call.
func (s *Source) FlipAfter(n int, version string, newData []byte, chunkSize int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flipAfter = n
	s.pending = &pendingFlip{version, newData, chunkSize}
}

type pendingFlip struct {
	version   string
	data      []byte
	chunkSize int
}

// ReadAt serves one chunk-sized frame region through a fragmenting reader.
func (s *Source) ReadAt(ctx context.Context, version string, off, length int64) ([]byte, error) {
	s.mu.Lock()
	data, ok := s.versions[version]
	if !ok {
		s.mu.Unlock()
		return nil, &chunkfetch.VersionMismatchError{Wanted: version, Got: s.servedVersion()}
	}
	s.reads++
	if s.pending != nil && s.flipAfter > 0 && s.reads >= s.flipAfter {
		pf := s.pending
		s.pending = nil
		s.flipAfter = 0
		s.manifest = chunkfetch.BuildManifest(pf.version, pf.data, pf.chunkSize)
		s.versions[pf.version] = append([]byte(nil), pf.data...)
		s.mu.Unlock()
		return nil, &chunkfetch.VersionMismatchError{Wanted: version, Got: pf.version}
	}
	rng := s.rng
	maxFrag := s.maxFragment
	chunkIndex := int(off / int64(s.manifest.ChunkSize))
	flt := s.faults[chunkIndex]
	if flt != nil {
		s.readCalls[chunkIndex]++
	}
	seen := s.readCalls[chunkIndex]
	chunkSize := s.manifest.ChunkSize
	s.mu.Unlock()

	if off < 0 || off >= int64(len(data)) || off+length > int64(len(data)) {
		return nil, fmt.Errorf("simsource: range [%d,%d) out of bounds", off, off+length)
	}
	_ = chunkIndex
	_ = chunkSize

	frame := chunkfetch.EncodeFrame(data[off : off+length])
	if flt != nil {
		active := flt.Times == 0 || seen <= flt.Times
		if active {
			frame = applyFault(frame, flt.Kind, rng)
		}
	}

	dst := make([]byte, len(frame))
	if _, err := fragmentedFill(ctx, bytes.NewReader(frame), dst, rng, maxFrag); err != nil {
		// A truncated frame is still returned; the fetcher distinguishes
		// missing data from bad data at the protocol layer.
		return dst, err
	}
	return dst, nil
}

func (s *Source) attemptSoFar(chunk int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readCalls[chunk]
}

func (s *Source) servedVersion() string {
	return s.manifest.Version
}

func applyFault(frame []byte, kind FaultKind, rng *rand.Rand) []byte {
	switch kind {
	case FaultCorrupt:
		out := append([]byte(nil), frame...)
		if len(out) > 4+32 {
			pos := 4 + 32 + rng.Intn(len(out)-4-32)
			out[pos] ^= 0xFF
		}
		return out
	case FaultTruncated:
		cut := 4 + rng.Intn(len(frame)-4)
		return append([]byte(nil), frame[:cut]...)
	case FaultOversize:
		out := append([]byte(nil), frame...)
		binary.BigEndian.PutUint32(out[:4], uint32(len(frame))+1_000_000)
		return out
	}
	return frame
}

// fragmentedFill copies src into dst using reads of random small sizes to
// emulate a transport that delivers bytes in arbitrary pieces.
func fragmentedFill(ctx context.Context, src *bytes.Reader, dst []byte, rng *rand.Rand, maxFrag int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if maxFrag < 1 {
		maxFrag = 1
	}
	total := 0
	for total < len(dst) {
		limit := 1 + rng.Intn(maxFrag)
		if limit > len(dst)-total {
			limit = len(dst) - total
		}
		got, _ := src.Read(dst[total : total+limit])
		total += got
		if got == 0 {
			// Source ended before filling dst: a cut connection. The
			// caller's frame parser reports this as "need more".
			return total, errors.New("unexpected EOF")
		}
	}
	return total, nil
}
