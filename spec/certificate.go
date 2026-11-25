package spec

// StateKey 是具体内部状态的规范指纹（完整状态，规划器内部使用）。
func StateKey(s *State) (string, error) {
	return Fingerprint(s)
}

// ObservationKey 是公开观测的规范指纹；宿主与规划器都只能依据该键分支。
func ObservationKey(o *Observation) (string, error) {
	return Fingerprint(o)
}

// ActionKey 是动作的规范指纹，用于证书中的策略表与转移表。
func ActionKey(a *Action) (string, error) {
	return Fingerprint(a)
}

// BeliefNode 是证书中记录的一个信念（与具体内部状态无关的信息状态）。
//
// 信念只由公开观测历史决定；States 中列出所有与该观测历史相容的
// 具体内部状态，用以证明“同一读回对应多种内部状态时策略仍然相同”。
type BeliefNode struct {
	// States 是该信念覆盖的全部具体状态的规范指纹。
	States []string `json:"states"`
	// ObsKey 是产生该信念的最新观测指纹（根信念为空字符串）。
	ObsKey string `json:"obs_key"`
}

// PolicyEntry 是策略表的一行：给定信念与剩余烧录预算时选择的动作。
type PolicyEntry struct {
	BeliefKey      string `json:"belief_key"`
	BudgetLeft     int    `json:"budget_left"`
	ActionKey      string `json:"action_key"`
	WorstCaseBurns int    `json:"worst_case_burns"`
}

// TransitionEntry 描述某信念下执行某动作后，按公开观测得到的后继信念。
// ack 与 boot 各至多一条；ack 不携带内部信息，boot 按读回键分裂。
type TransitionEntry struct {
	BeliefKey string            `json:"belief_key"`
	ActionKey string            `json:"action_key"`
	Outcomes  map[string]string `json:"outcomes"`
}

// Certificate 是策略的可独立核验证书。
//
// 证书同时包含芯片规格与全部枚举证据，使核验器无需信任规划器，
// 仅凭本包的数据类型即可重建状态空间并重新求解最小最坏烧录次数。
type Certificate struct {
	SchemaVersion int   `json:"schema_version"`
	Chip          *Chip `json:"chip"`
	// RootBeliefKey 是上电起点信念的指纹。
	RootBeliefKey string `json:"root_belief_key"`
	// Beliefs 是枚举到的全部信念，键为信念指纹。
	Beliefs map[string]*BeliefNode `json:"beliefs"`
	// Actions 是证书引用到的全部具体动作，键为动作指纹。
	Actions map[string]*Action `json:"actions"`
	// Observations 是证书引用到的全部观测，键为观测指纹。
	Observations map[string]*Observation `json:"observations"`
	// States 是枚举到的全部具体状态，键为状态指纹。
	States map[string]*State `json:"states"`
	// Transitions 是信念级转移证据（动作 -> 观测 -> 后继信念）。
	Transitions []*TransitionEntry `json:"transitions"`
	// Policy 是最小最坏烧录策略表。
	Policy []*PolicyEntry `json:"policy"`
	// WorstCaseBurns 是根信念在完整预算下的最小最坏烧录次数。
	WorstCaseBurns int `json:"worst_case_burns"`
	// SpecFingerprint 是芯片规格的规范指纹，便于宿主比对。
	SpecFingerprint string `json:"spec_fingerprint"`
}
