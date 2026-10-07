package paradl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
)

// Response is what a source returns for one range request. It mirrors an
// HTTP partial-content reply: the entity version (ETag) and the actual
// range the body covers, which may be smaller than requested.
type Response struct {
	Version  string    // entity version of the content
	Range    ByteRange // actual range covered by Data
	Data     []byte    // exactly Range.Len() bytes
	Checksum string    // sha256 hex of Data, like a Content-MD5 style header
}

// Source is a fetch endpoint for ranged reads.
type Source interface {
	// Head returns the current size and entity version.
	Head(ctx context.Context) (size int64, version string, err error)
	// Fetch returns the content for r. The returned range may be smaller
	// than requested (short write) and the version may be stale.
	Fetch(ctx context.Context, r ByteRange) (Response, error)
}

// ChecksumBytes returns the sha256 hex of b.
func ChecksumBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ChecksumReaderAt reads [start, end) from r and returns its sha256 hex.
func ChecksumReaderAt(r io.ReaderAt, start, end int64) (string, error) {
	h := sha256.New()
	if _, err := copyRange(h, r, start, end); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyRange(w io.Writer, r io.ReaderAt, start, end int64) (int64, error) {
	sr := io.NewSectionReader(r, start, end-start)
	return io.Copy(w, sr)
}

// LocalSource simulates a remote file server in memory. Content is generated
// deterministically from a seed so tests can predict checksums. Faults from
// the fault file are applied to requests in arrival order.
type LocalSource struct {
	mu       sync.Mutex
	data     []byte
	version  string
	seed     int64
	vcounter int

	faults   *FaultFile
	requests int
	stale    map[int]Response // request number -> stale snapshot to serve
}

// NewLocalSource builds a source of size bytes with deterministic content.
func NewLocalSource(seed, size int64, version string, faults *FaultFile) *LocalSource {
	if faults == nil {
		faults = &FaultFile{}
	}
	s := &LocalSource{
		version: version,
		seed:    seed,
		faults:  faults,
		stale:   make(map[int]Response),
	}
	s.data = fillDeterministic(seed, size)
	return s
}

// fillDeterministic generates pseudo-random but reproducible content.
func fillDeterministic(seed, size int64) []byte {
	out := make([]byte, size)
	state := uint64(seed)*2654435761 + 1
	for i := range out {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		out[i] = byte(state)
	}
	return out
}

// Head implements Source.
func (s *LocalSource) Head(ctx context.Context) (int64, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.data)), s.version, nil
}

// Fetch implements Source, applying faults in request order.
func (s *LocalSource) Fetch(ctx context.Context, r ByteRange) (Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests++
	n := s.requests

	// A queued stale snapshot takes over this request slot.
	if resp, ok := s.stale[n]; ok {
		delete(s.stale, n)
		return resp, nil
	}

	// Version change faults apply before serving.
	for _, vc := range s.faults.VersionChanges {
		if vc.OnRequest == n {
			s.applyVersionChangeLocked(vc)
		}
	}

	// Capture a snapshot for a future stale response.
	for _, sf := range s.faults.StaleResponses {
		if sf.OnRequest == n {
			snap := s.serveLocked(r)
			s.stale[n+sf.DelayRequests] = snap
		}
	}

	resp := s.serveLocked(r)

	// Short write faults truncate the actual range.
	for _, sw := range s.faults.ShortWrites {
		if sw.OnRequest == n && sw.KeepBytes < resp.Range.Len() {
			if sw.KeepBytes < 0 {
				resp.Range.End = resp.Range.Start
			} else {
				resp.Range.End = resp.Range.Start + sw.KeepBytes
			}
			resp.Data = resp.Data[:resp.Range.Len()]
			resp.Checksum = ChecksumBytes(resp.Data)
		}
	}
	return resp, nil
}

// serveLocked builds the response for r against current content.
func (s *LocalSource) serveLocked(r ByteRange) Response {
	end := min(r.End, int64(len(s.data)))
	start := min(r.Start, end)
	data := make([]byte, end-start)
	copy(data, s.data[start:end])
	return Response{
		Version:  s.version,
		Range:    ByteRange{Start: start, End: end},
		Data:     data,
		Checksum: ChecksumBytes(data),
	}
}

func (s *LocalSource) applyVersionChangeLocked(vc VersionChangeFault) {
	size := int64(len(s.data))
	if vc.NewSize > 0 {
		size = vc.NewSize
	}
	s.seed = vc.NewSeed
	s.data = fillDeterministic(vc.NewSeed, size)
	s.vcounter++
	if vc.NewVersion != "" {
		s.version = vc.NewVersion
	} else {
		s.version = fmt.Sprintf("v%d", s.vcounter)
	}
}

// Content returns a copy of the current content (test helper).
func (s *LocalSource) Content() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, len(s.data))
	copy(out, s.data)
	return out
}

// Version returns the current entity version (test helper).
func (s *LocalSource) Version() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Requests returns how many Fetch calls were made (test helper).
func (s *LocalSource) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}
