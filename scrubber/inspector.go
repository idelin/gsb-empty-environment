package scrubber

// Verifier reports whether a payload is intact.
type Verifier func(data []byte) bool

// Outcome describes what an inspection concluded.
type Outcome int

const (
	// Healthy means the payload verified and the read version was marked.
	Healthy Outcome = iota
	// Corrupt means the payload failed verification and the read version
	// was quarantined.
	Corrupt
	// Stale means the object was updated between read and mark; the
	// result expired and the newer version was left untouched.
	Stale
	// Gone means the object was deleted between read and mark, or was
	// never there.
	Gone
	// AlreadyQuarantined means the object is already quarantined.
	AlreadyQuarantined
)

func (o Outcome) String() string {
	switch o {
	case Healthy:
		return "healthy"
	case Corrupt:
		return "corrupt"
	case Stale:
		return "stale"
	case Gone:
		return "gone"
	case AlreadyQuarantined:
		return "already-quarantined"
	}
	return "unknown"
}

// Inspector scrubs objects in a Backend. It captures the object version
// at read time and only ever marks that exact version; the conditional
// mark and the version comparison execute atomically in the backend, so
// a concurrent update or delete can never cause the new object to be
// quarantined by an expired result.
type Inspector struct {
	backend Backend
	verify  Verifier
}

// NewInspector creates an Inspector reading from b and checking payloads
// with verify.
func NewInspector(b Backend, verify Verifier) *Inspector {
	return &Inspector{backend: b, verify: verify}
}

// Inspect reads key, verifies its payload, and marks the captured
// version healthy or corrupt. If the object changed between the read
// and the mark, the outcome is Stale (updated) or Gone (deleted) and
// the current object is guaranteed to be unmodified.
func (i *Inspector) Inspect(key string) (Outcome, error) {
	obj, err := i.backend.Get(key)
	if err == ErrNotFound {
		return Gone, nil
	}
	if err == ErrQuarantined {
		return AlreadyQuarantined, nil
	}
	if err != nil {
		return 0, err
	}

	intact := i.verify(obj.Data)
	if intact {
		err = i.backend.MarkHealthyIfVersion(key, obj.Version)
	} else {
		err = i.backend.QuarantineIfVersion(key, obj.Version)
	}
	switch err {
	case nil:
		if intact {
			return Healthy, nil
		}
		return Corrupt, nil
	case ErrStaleVersion:
		return Stale, nil
	case ErrNotFound:
		return Gone, nil
	}
	return 0, err
}
