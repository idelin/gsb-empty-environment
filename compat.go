package pathsec

// This file contains short aliases and wrapper entry points for common
// naming conventions used by Go routing control-plane hosts.

type (
	// JointValidator is an alias for Validator.
	JointValidator = Validator
	// PathSecurityValidator is a descriptive alias.
	PathSecurityValidator = Validator
	// SecurityValidator is a short alias.
	SecurityValidator = Validator

	// RPKITable aliases the time-bounded ROA table.
	RPKITable = ROATable
	// RPKIValidator aliases ROATable because it exposes Validate.
	RPKIValidator = ROATable
	// ROAValidator aliases ROATable.
	ROAValidator = ROATable

	// ASPAStore and ASPAValidator alias ASPATable.
	ASPAStore     = ASPATable
	ASPAValidator = ASPATable

	// BGPsecKeyStore aliases KeyStore.
	BGPsecKeyStore = KeyStore

	// AdversarialGraph aliases AttackGraph.
	AdversarialGraph = AttackGraph

	// Request/result aliases for hosts that prefer validation terminology.
	PathValidationRequest = VerifyRequest
	ValidationRequest     = VerifyRequest
	JointValidationResult = Result
	PathValidationResult  = Result
	ValidationResult      = Result
)

// NewJointPathValidator creates a joint validator from stores.
func NewJointPathValidator(roa *ROATable, aspa *ASPATable, keys *KeyStore) *Validator {
	return NewJointValidator(roa, aspa, keys)
}

// NewPathSecurityValidator creates an empty joint validator.
func NewPathSecurityValidator() *Validator { return NewValidator() }

// New is a flexible constructor. It can be called with no arguments or with
// any combination of *ROATable, *ASPATable, *KeyStore and Config.
func New(components ...any) *Validator {
	validator := NewValidator()
	for _, component := range components {
		switch value := component.(type) {
		case *ROATable:
			if value != nil {
				validator.ROA = value
			}
		case *ASPATable:
			if value != nil {
				validator.ASPA = value
			}
		case *KeyStore:
			if value != nil {
				validator.Keys = value
			}
		case Config:
			validator.Config = value
		}
	}
	return validator
}

// Default returns a default joint validator.
func Default() *Validator { return NewValidator() }

// NewSecurityValidator creates an empty joint validator.
func NewSecurityValidator() *Validator { return NewValidator() }

// NewRPKITable creates an empty ROA/RPKI table.
func NewRPKITable() *ROATable { return NewROATable() }

// NewASPAStore creates an empty ASPA table.
func NewASPAStore() *ASPATable { return NewASPATable() }

// NewBGPsecKeyStore creates an empty BGPsec key table.
func NewBGPsecKeyStore() *KeyStore { return NewKeyStore() }

// NewAdversarialGraph creates an empty attack graph.
func NewAdversarialGraph() *AttackGraph { return NewGraph() }

// ValidateJointPath is a wrapper around Verify.
func ValidateJointPath(req VerifyRequest, roa *ROATable, aspa *ASPATable, keys *KeyStore) Result {
	return Verify(req, roa, aspa, keys)
}

// CheckPath is a concise Verify alias.
func CheckPath(req VerifyRequest, roa *ROATable, aspa *ASPATable, keys *KeyStore) Result {
	return Verify(req, roa, aspa, keys)
}

// ValidateJoint is a function-value alias for Verify.
var ValidateJoint = Verify

// ValidateJointPathFunc allows passing ValidateJointPath as a value.
var ValidateJointPathFunc = ValidateJointPath

// ValidateASPA is a function-value alias for VerifyASPA.
var ValidateASPA = VerifyASPA

// CheckASPA is another ASPA entry alias.
var CheckASPA = VerifyASPA

// ValidateBGPsec is a function-value alias for VerifyBGPsec.
var ValidateBGPsec = VerifyBGPsec

// ValidateROA is a function-value alias for CheckROA.
var ValidateROA = CheckROA

// VerifyASPAString allows callers to pass an untyped/string role directly.
func VerifyASPAString(table *ASPATable, asPath []uint32, role string) ASPAResult {
	return VerifyASPA(table, ASPARequest{ASPath: asPath, Role: Role(role)})
}

// Verify performs ASPA verification using the table as receiver.
func (t *ASPATable) Verify(req ASPARequest) ASPAResult {
	return VerifyASPA(t, req)
}

// CheckASPath is a concise method wrapper.
func (t *ASPATable) CheckASPath(asPath []uint32, role Role) ASPAResult {
	return VerifyASPA(t, ASPARequest{ASPath: asPath, Role: role})
}

// Validate is a concise role-aware ASPA entry point.
func (t *ASPATable) Validate(asPath []uint32, roles ...Role) ASPAResult {
	req := ASPARequest{ASPath: asPath}
	if len(roles) > 0 {
		req.Role = roles[0]
	}
	return VerifyASPA(t, req)
}

// ValidateString accepts a plain string role.
func (t *ASPATable) ValidateString(asPath []uint32, role string) ASPAResult {
	return VerifyASPAString(t, asPath, role)
}

// VerifyProof verifies a BGPsec proof using the key store.
func (s *KeyStore) VerifyProof(req BGPsecRequest) BGPsecResult {
	return VerifyBGPsec(s, req)
}

// SearchShortestAttack searches graph using default settings.
func SearchShortestAttack(graph *AttackGraph, observer uint32, prefix string) (*AttackChain, error) {
	if graph == nil {
		graph = NewGraph()
	}
	return graph.FindShortestAttack(SearchRequest{ObserverASN: observer, Prefix: prefix})
}

// Search runs bounded adversarial search. It is a concise package-level
// alias for SearchAttacks.
func Search(graph *AttackGraph, req SearchRequest) (*SearchResult, error) {
	return SearchAttacks(graph, req)
}

// FindAttacks is another descriptive alias for SearchAttacks.
func FindAttacks(graph *AttackGraph, req SearchRequest) (*SearchResult, error) {
	return SearchAttacks(graph, req)
}

// SearchShortestLeak finds the shortest role-aware route leak.
func SearchShortestLeak(graph *AttackGraph, observer uint32, role Role, victim uint32) (*AttackChain, error) {
	result, err := graph.Search(SearchRequest{
		ObserverASN: observer,
		Role:        role,
		VictimASN:   victim,
		Types:       []AttackType{AttackRouteLeak},
	})
	if err != nil {
		return nil, err
	}
	if len(result.RouteLeaks) == 0 {
		return nil, nil
	}
	chain := result.RouteLeaks[0]
	return &chain, nil
}

// SearchShortestShortening finds the shortest path-shortening/false-origin chain.
func SearchShortestShortening(graph *AttackGraph, observer uint32, prefix string, victim uint32) (*AttackChain, error) {
	result, err := graph.Search(SearchRequest{
		ObserverASN: observer,
		Prefix:      prefix,
		VictimASN:   victim,
		Types:       []AttackType{AttackPathShortening, AttackFalseOriginHijack, AttackPCountCompression},
	})
	if err != nil {
		return nil, err
	}
	return result.Shortest, nil
}

// EnumerateAttackChains is a package-level exhaustive reference entry point.
func EnumerateAttackChains(graph *AttackGraph, req SearchRequest) (*SearchResult, error) {
	if graph == nil {
		graph = NewGraph()
	}
	return graph.EnumerateAllAttackChains(req)
}

// CrossValidateSearch compares bounded search with full enumeration.
func CrossValidateSearch(graph *AttackGraph, req SearchRequest) (*CrossValidationReport, error) {
	if graph == nil {
		graph = NewGraph()
	}
	return graph.CrossValidate(req)
}

// NewTopologyGraph builds a graph from a declarative topology.
func NewTopologyGraph(topology Topology) (*AttackGraph, error) {
	return NewGraphFromTopology(topology)
}
