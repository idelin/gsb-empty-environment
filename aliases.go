package netsec

// Compatibility and convenience aliases kept intentionally small.  They use
// the same underlying types and semantics as the primary API.

// DedupPolicy and Policy are aliases for MergePolicy.
type DedupPolicy = MergePolicy
type Policy = MergePolicy

// ProbeSequence is an alias for Sequence.
type ProbeSequence = Sequence

// Candidate and CandidateSecret are aliases for Secret.
type Candidate = Secret
type CandidateSecret = Secret

// CostRange and CostSpan are aliases for CostInterval.
type CostRange = CostInterval
type CostSpan = CostInterval

// Event is an alias for EventType.
type Event = EventType

const (
	AccessEvent       = EventAccess
	PrivateWriteEvent = EventPrivateWrite
	COWEvent          = EventCOW
	COWWriteEvent     = EventCOW
	COWFailureEvent   = EventCOWFailure
)

// Convenient event-cost constants.
const (
	COWCost          = DefaultCOWCost
	PrivateWriteCost = DefaultPrivateWriteCost
	OtherAccessCost  = DefaultAccessCost
)

// DefaultDedupPolicy, HardenedDedupPolicy and GlobalInsecurePolicy expose the
// built-in policies as values.
var (
	DefaultDedupPolicy   MergePolicy = ConservativePolicy{}
	HardenedDedupPolicy  MergePolicy = HardenedPolicy{}
	GlobalInsecurePolicy MergePolicy = GlobalDedupPolicy{}
)

// CanMerge applies the conservative policy to two page snapshots.
func CanMerge(incoming, resident Page) bool {
	return ConservativePolicy{}.CanMerge(incoming, resident)
}

// NewMemoryWithPolicy constructs a host with an explicit policy.
func NewMemoryWithPolicy(policy any, costs ...CostModel) *Memory {
	cfg := Config{Policy: policy}
	if len(costs) > 0 {
		cfg.Costs = costs[0]
	}
	return NewMemory(cfg)
}

// NewHostWithPolicy constructs a host with an explicit policy.
func NewHostWithPolicy(policy any, costs ...CostModel) *Host {
	return NewMemoryWithPolicy(policy, costs...)
}

// Create installs a page from its common fields.
func (m *Memory) Create(domain DomainID, content []byte, shareable bool, tags ...string) PageRef {
	return m.CreatePage(PageSpec{
		DomainID:  domain,
		Content:   content,
		Tags:      tags,
		Shareable: shareable,
	})
}

// CreateString is Create for string content.
func (m *Memory) CreateString(domain DomainID, content string, shareable bool, tags ...string) PageRef {
	return m.Create(domain, []byte(content), shareable, tags...)
}

// WriteString is Write for string data.
func (m *Memory) WriteString(ref PageRef, data string) (WriteResult, error) {
	return m.Write(ref, []byte(data))
}

// ReadString reads a page and returns its content as a string.
func (m *Memory) ReadString(ref PageRef) (string, error) {
	result, err := m.Read(ref)
	if err != nil {
		return "", err
	}
	return string(result.Content), nil
}

// InjectCOWFailure makes the next COW fail.  It is an alias for FailNextCOW.
func (m *Memory) InjectCOWFailure(err ...error) { m.FailNextCOW(err...) }
