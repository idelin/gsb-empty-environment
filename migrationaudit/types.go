package migrationaudit

type AccountID string
type DigestID string
type FamilyID string
type ObserverGroupID string
type ReplicaID string
type SaltDomain string
type SlotID string

type Cost int

type ActionKind string

const (
	ActionMove         ActionKind = "move"
	ActionOverwrite    ActionKind = "overwrite"
	ActionErase        ActionKind = "erase"
	ActionChangePolicy ActionKind = "change_policy"
)

type Digest struct {
	ID         DigestID
	SaltDomain SaltDomain
	Family     FamilyID
	Cost       Cost
	Accounts   []AccountID
}

type Slot struct {
	ID            SlotID
	Replica       ReplicaID
	InitialDigest DigestID
}

type FamilyMembership struct {
	Family     FamilyID
	SaltDomain SaltDomain
	Accounts   []AccountID
}

type ObserverGroup struct {
	ID              ObserverGroupID
	InitialReplicas []ReplicaID
}

type Action struct {
	ID        int
	Kind      ActionKind
	DependsOn []int

	Digest   DigestID
	Source   SlotID
	Target   SlotID
	Group    ObserverGroupID
	Replicas []ReplicaID
}

type Problem struct {
	TargetCost     Cost
	Digests        []Digest
	Slots          []Slot
	Families       []FamilyMembership
	ObserverGroups []ObserverGroup
	Actions        []Action
	ValidatorSlots []SlotID
}

type FamilySaltCost struct {
	Family      FamilyID
	SaltDomain  SaltDomain
	MinimumCost Cost
}

type GroupExposureSnapshot struct {
	GroupID                ObserverGroupID
	ObservedDigests        []DigestID
	LowCostObservedDigests []DigestID
	ExposedAccounts        []AccountID
	MinimumCosts           []FamilySaltCost
}

type PrefixSnapshot struct {
	Step                     int
	ActionID                 int
	ExecutedActionIDs        []int
	WorstGroupID             ObserverGroupID
	WorstExposedAccountCount int
	GroupExposures           []GroupExposureSnapshot
}

type SearchStats struct {
	ReachableStates              int
	FeasibilityCertificateStates int
	OptimizationStates           int
	TransitionChecks             int
	PrunedByExposureCap          int
}

type FeasibilityCertificateEntry struct {
	StateKey       string
	Feasible       bool
	ChosenActionID int
	LegalActionIDs []int
}

type OptimizationCertificateEntry struct {
	StateKey             string
	Feasible             bool
	RemainingExposureSum int
	ChosenActionID       int
	TransitionExposure   int
	SuccessorStateKey    string
	LegalActionIDs       []int
}

type Certificate struct {
	TargetPeak          int
	LowerCap            int
	LowerCapFeasibility map[string]FeasibilityCertificateEntry
	Optimization        map[string]OptimizationCertificateEntry
	InitialStateKey     string
}

type Result struct {
	Feasible                       bool
	InfeasibleReason               string
	Order                          []int
	Prefixes                       []PrefixSnapshot
	Peak                           int
	PeakStep                       int
	PeakGroupID                    ObserverGroupID
	PeakDigestIDs                  []DigestID
	PeakAccountIDs                 []AccountID
	TotalPrefixExposure            int
	InitialInclusivePrefixExposure int
	Certificate                    *Certificate
	Stats                          SearchStats
}
