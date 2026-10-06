package dl

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// Manifest describes the current entity on the origin: its version and the
// per-chunk digests the client uses to verify confirmed ranges.
type Manifest struct {
	ETag      string
	Size      int64
	ChunkSize int64
	Hashes    []string // hex sha256, one per chunk
}

// Response models one HTTP range response: it carries the entity version
// and the actual range the server chose to return. Data may be shorter
// than the declared range (short write).
type Response struct {
	ETag        string
	Start       int64
	DeclaredEnd int64 // inclusive, as claimed in Content-Range
	Data        []byte
}

// Server is a local mock origin holding a versioned entity.
type Server struct {
	mu       sync.Mutex
	content  []byte
	etag     string
	version  int
	chunk    int64
	faults   *Faults
	reqCount int
	stale    *Response // first response ever served, kept for stale replay
}

// NewServer builds an origin serving content in chunkSize chunks.
func NewServer(content []byte, chunkSize int64, faults *Faults) *Server {
	if faults == nil {
		faults = &Faults{}
	}
	s := &Server{chunk: chunkSize, faults: faults, version: 1}
	s.setContent(content)
	return s
}

func (s *Server) setContent(content []byte) {
	s.content = make([]byte, len(content))
	copy(s.content, content)
	s.etag = fmt.Sprintf("v%d", s.version)
}

// Requests returns how many fetches the server has served (test aid).
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reqCount
}

// Content returns the current entity bytes (test aid).
func (s *Server) Content() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, len(s.content))
	copy(out, s.content)
	return out
}

// Manifest returns the manifest of the current entity version.
func (s *Server) Manifest() Manifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manifestLocked()
}

func (s *Server) manifestLocked() Manifest {
	n := int((int64(len(s.content)) + s.chunk - 1) / s.chunk)
	m := Manifest{ETag: s.etag, Size: int64(len(s.content)), ChunkSize: s.chunk, Hashes: make([]string, n)}
	for i := 0; i < n; i++ {
		start := int64(i) * s.chunk
		end := start + s.chunk
		if end > int64(len(s.content)) {
			end = int64(len(s.content))
		}
		sum := sha256.Sum256(s.content[start:end])
		m.Hashes[i] = hex.EncodeToString(sum[:])
	}
	return m
}

// Fetch serves one range request [start, end] (inclusive). It normally
// returns exactly one response; fault injection may truncate, expand or
// corrupt it, rotate the entity version, or attach a stale replay.
func (s *Server) Fetch(start, end int64) []Response {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.reqCount++
	reqNo := s.reqCount

	if s.faults.shouldRotate(reqNo) {
		s.version++
		rotated := make([]byte, len(s.content))
		for i, b := range s.content {
			rotated[i] = b ^ 0xFF
		}
		s.setContent(rotated)
	}

	if end >= int64(len(s.content)) {
		end = int64(len(s.content)) - 1
	}
	resp := Response{ETag: s.etag, Start: start, DeclaredEnd: end}
	resp.Data = make([]byte, end-start+1)
	copy(resp.Data, s.content[start:end+1])

	if s.faults.intIn(s.faults.ExpandRangeOnRequests, reqNo) {
		resp.Start = 0
		resp.DeclaredEnd = int64(len(s.content)) - 1
		resp.Data = make([]byte, len(s.content))
		copy(resp.Data, s.content)
	}
	if s.faults.intIn(s.faults.CorruptOnRequests, reqNo) && len(resp.Data) > 0 {
		resp.Data[0] ^= 0xFF
	}
	if s.faults.intIn(s.faults.ShortWriteOnRequests, reqNo) && len(resp.Data) > 1 {
		resp.Data = resp.Data[:len(resp.Data)/2] // declared range kept, body short
	}

	if s.stale == nil {
		cp := Response{ETag: resp.ETag, Start: resp.Start, DeclaredEnd: resp.DeclaredEnd}
		cp.Data = make([]byte, len(resp.Data))
		copy(cp.Data, resp.Data)
		s.stale = &cp
	}

	resps := []Response{resp}
	if s.faults.shouldReplayStale(reqNo) && s.stale.ETag != s.etag {
		resps = append(resps, *s.stale)
	}
	return resps
}
