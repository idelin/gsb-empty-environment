package crush

// Common alternate names for the primary public types.
type (
	CRUSH      = Mapper
	Crush      = Mapper
	CRUSHMap   = Mapper
	CrushMap   = Mapper
	ClusterMap = Mapper

	Topology     = Node
	TopologyNode = Node
	CrushNode    = Node
	ClusterNode  = Node
	TreeNode     = Node
	Bucket       = Node
	OSD          = Node

	CrushRule = Rule
	RuleStep  = Step

	Hash = HashFunc
)

// StepType/Operation aliases used by some rule literals.
const (
	StepTypeTake       = OpTake
	StepTypeChoose     = OpChoose
	StepTypeChooseLeaf = OpChooseLeaf
	StepTypeEmit       = OpEmit

	OperationTake       = OpTake
	OperationChoose     = OpChoose
	OperationChooseLeaf = OpChooseLeaf
	OperationEmit       = OpEmit

	RuleOpTake       = OpTake
	RuleOpChoose     = OpChoose
	RuleOpChooseLeaf = OpChooseLeaf
	RuleOpEmit       = OpEmit

	RetryModeFirstN = FirstN
	RetryModeIndep  = Indep
	StrategyFirstN  = FirstN
	StrategyIndep   = Indep
)

// NewCRUSH is an alternate constructor name.
func NewCRUSH(args ...any) *CRUSH {
	return NewMapper(args...)
}

// NewCrush is an alternate constructor name.
func NewCrush(args ...any) *CRUSH {
	return NewMapper(args...)
}

// NewCRUSHMap is an alternate constructor name.
func NewCRUSHMap(args ...any) *CRUSH {
	return NewMapper(args...)
}

// NewCrushMap is an alternate constructor name.
func NewCrushMap(args ...any) *CRUSH {
	return NewMapper(args...)
}

// NewClusterMap is an alternate constructor name.
func NewClusterMap(args ...any) *CRUSH {
	return NewMapper(args...)
}

// MapPG is a package-level convenience entry point. It accepts the common
// argument orderings: a mapper/root, optional hash/rule/rule name, PG id, and
// replica count.
func MapPG(args ...any) (*Result, error) {
	parsed, err := parseMapArgs(args)
	if err != nil {
		return nil, err
	}
	return parsed.mapper.MapPG(parsed.pgID, parsed.replicas, parsed.options...)
}

// Map is an alias for the package-level MapPG convenience function.
func Map(args ...any) (*Result, error) {
	return MapPG(args...)
}

// MapOSDs behaves like MapPG but returns only the OSD slice.
func MapOSDs(args ...any) ([]int, error) {
	result, err := MapPG(args...)
	if result == nil {
		return nil, err
	}
	return result.OSDs, err
}

type parsedMapArgs struct {
	mapper   *Mapper
	pgID     any
	replicas int
	options  []any
}

func parseMapArgs(args []any) (parsedMapArgs, error) {
	parsed := parsedMapArgs{replicas: -1}
	var root *Node
	var hasher any
	var rule *Rule
	var integers []uint64
	var integerKinds []string

	for _, arg := range args {
		switch value := arg.(type) {
		case nil:
			continue
		case *Mapper:
			parsed.mapper = value
		case Mapper:
			parsed.mapper = &value
		case *Node:
			root = value
		case Node:
			root = &value
		case *Rule:
			rule = value
		case Rule:
			rule = &value
		case string:
			parsed.options = append(parsed.options, value)
		case int:
			integers = append(integers, uint64(value))
			integerKinds = append(integerKinds, "int")
		case int64:
			integers = append(integers, uint64(value))
			integerKinds = append(integerKinds, "int64")
		case uint64:
			integers = append(integers, value)
			integerKinds = append(integerKinds, "uint64")
		case uint:
			integers = append(integers, uint64(value))
			integerKinds = append(integerKinds, "uint")
		default:
			if h, err := adaptHasher(arg); err == nil {
				hasher = arg
				_ = h
			}
		}
	}

	if parsed.mapper == nil {
		if root == nil {
			return parsedMapArgs{}, ErrNoTopology
		}
		rules := []any{}
		if rule != nil {
			rules = append(rules, rule)
		}
		parsed.mapper = NewMapper(append([]any{root, hasher}, rules...)...)
	} else if rule != nil {
		parsed.options = append(parsed.options, rule)
	}

	if len(integers) == 0 {
		return parsedMapArgs{}, &InvalidPGIDError{ID: nil}
	}
	parsed.pgID = integers[0]
	if len(integers) >= 2 {
		parsed.replicas = int(integers[1])
	}
	if parsed.replicas < 0 {
		parsed.replicas = 0
	}
	return parsed, nil
}
