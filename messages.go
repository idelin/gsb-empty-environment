package causalslice

// VersionID uniquely identifies a write by its origin replica and its
// per-origin sequence number. Equivalent to a (actor, counter) event.
type VersionID struct {
	Origin string
	Seq    int
}

func (v VersionID) String() string { return v.Origin + "#" + itoa(v.Seq) }

// IsZero reports whether the id is unset.
func (v VersionID) IsZero() bool { return v.Origin == "" && v.Seq == 0 }

// Version is one causally tagged object version. Deps lists the exact causal
// past ("why I may be read"), VC is its vector clock ("where I sit in the
// partial order").
type Version struct {
	ID   VersionID
	Key  string
	Val  string
	Deps []VersionID
	VC   map[string]int
}

// PutReq is a client write request to a replica. KnownDeps is the client's
// causal context; the replica rejects the write unless it holds every dep.
type PutReq struct {
	Key       string
	Val       string
	KnownDeps []VersionID
	Tag       interface{}
}

// PutAck reports the newly assigned version, or ErrCauseMissing.
type PutAck struct {
	Key string
	Ver *Version
	Err error
	Tag interface{}
}

// FetchReq asks a replica for either the latest version of Key (when Dep is
// zero) or one exact version Dep. Token is opaque state owned by the
// requesting snapshot.
type FetchReq struct {
	Key   string
	Dep   VersionID
	Token interface{}
}

// FetchResp returns the selected version (nil if unknown), every locally
// available ancestor in Closure, and the missing ancestor frontier.
type FetchResp struct {
	Key     string
	Ver     *Version
	Closure []*Version
	Missing []VersionID
	Token   interface{}
}

// Replicate pushes a single version along the gossip graph.
type Replicate struct{ Ver *Version }

// GossipReq is a periodic anti-entropy digest request.
type GossipReq struct{}

// GossipResp carries every version the sending replica knows.
type GossipResp struct{ Vers []*Version }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
