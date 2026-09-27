package desim

// Category orders events that share the same tick. Lower categories
// run first; ties inside a category are broken by sequence number.
type Category uint8

const (
	// CatTimer is for timer expirations (e.g. packet generation).
	CatTimer Category = iota
	// CatArrival is for packets arriving at a queue or sink.
	CatArrival
	// CatLink is for link-level completions (transmission done).
	CatLink
)

func (c Category) String() string {
	switch c {
	case CatTimer:
		return "timer"
	case CatArrival:
		return "arrival"
	case CatLink:
		return "link"
	}
	return "unknown"
}

// Event is a single scheduled occurrence. Ordering is the total order
// (Tick, Cat, Seq); Seq is unique per event, so the order is total and
// deterministic.
type Event struct {
	Tick uint64
	Cat  Category
	Seq  uint64
	Type string
	Data interface{}

	slot int
	gen  uint32
}

// Handle is a generational handle to a scheduled event. It can be used
// to cancel the event exactly once. Slots are recycled after an event
// is consumed, and the generation is bumped, so a stale Handle can
// never cancel (or otherwise affect) a newer event that reuses the
// same slot.
type Handle struct {
	slot  int
	gen   uint32
	valid bool
}

// Valid reports whether the handle refers to a slot that was ever
// allocated. A zero Handle is invalid.
func (h Handle) Valid() bool { return h.valid }
