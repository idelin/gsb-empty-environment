package crush

import "strings"

type Mode string

const (
	FirstN Mode = "firstn"
	Indep  Mode = "indep"

	ModeFirstN = FirstN
	ModeIndep  = Indep
)

const (
	OpTake       = "take"
	OpChoose     = "choose"
	OpChooseLeaf = "chooseleaf"
	OpEmit       = "emit"

	StepTake       = OpTake
	StepChoose     = OpChoose
	StepChooseLeaf = OpChooseLeaf
	StepEmit       = OpEmit
)

// Rule is an ordered CRUSH rule.
type Rule struct {
	ID      int    `json:"id,omitempty"`
	Name    string `json:"name"`
	Mode    Mode   `json:"mode,omitempty"`
	MinSize int    `json:"min_size,omitempty"`
	MaxSize int    `json:"max_size,omitempty"`
	Steps   []Step `json:"steps"`
}

// Step describes one take/choose/chooseleaf/emit rule step. Many aliases are
// accepted so externally authored rule literals can use either CRUSH-style or
// descriptive names.
type Step struct {
	Op        string `json:"op,omitempty"`
	Operation string `json:"operation,omitempty"`
	Action    string `json:"action,omitempty"`
	Command   string `json:"command,omitempty"`

	Mode     Mode   `json:"mode,omitempty"`
	Strategy string `json:"strategy,omitempty"`

	Count    int `json:"count,omitempty"`
	Number   int `json:"number,omitempty"`
	N        int `json:"n,omitempty"`
	Num      int `json:"num,omitempty"`
	Replicas int `json:"replicas,omitempty"`
	Size     int `json:"size,omitempty"`

	Type          string `json:"type,omitempty"`
	BucketType    string `json:"bucket_type,omitempty"`
	FailureDomain string `json:"failure_domain,omitempty"`
	Domain        string `json:"domain,omitempty"`

	Take   any `json:"take,omitempty"`
	Item   any `json:"item,omitempty"`
	Target any `json:"target,omitempty"`
	Root   any `json:"root,omitempty"`
	ID     any `json:"id,omitempty"`
	NodeID any `json:"node_id,omitempty"`

	TakeID   int    `json:"take_id,omitempty"`
	ItemID   int    `json:"item_id,omitempty"`
	TargetID int    `json:"target_id,omitempty"`
	RootID   int    `json:"root_id,omitempty"`
	NodeIDV  int    `json:"node_id_v,omitempty"`
	TakeName string `json:"take_name,omitempty"`
	ItemName string `json:"item_name,omitempty"`
	RootName string `json:"root_name,omitempty"`

	MaxAttempts     int `json:"max_attempts,omitempty"`
	Attempts        int `json:"attempts,omitempty"`
	Tries           int `json:"tries,omitempty"`
	MaxTries        int `json:"max_tries,omitempty"`
	TotalTries      int `json:"total_tries,omitempty"`
	MaxRetries      int `json:"max_retries,omitempty"`
	Retries         int `json:"retries,omitempty"`
	Retry           int `json:"retry,omitempty"`
	RetryCount      int `json:"retry_count,omitempty"`
	RetryLimit      int `json:"retry_limit,omitempty"`
	RetryTimes      int `json:"retry_times,omitempty"`
	MaxRetryTimes   int `json:"max_retry_times,omitempty"`
	AttemptCount    int `json:"attempt_count,omitempty"`
	MaxAttemptCount int `json:"max_attempt_count,omitempty"`
	ChooseTries     int `json:"choose_tries,omitempty"`
	LocalTries      int `json:"local_tries,omitempty"`
	LocalAttempts   int `json:"local_attempts,omitempty"`
	FallbackTries   int `json:"fallback_tries,omitempty"`
}

// NewRule creates a named rule.
func NewRule(name string, steps ...Step) *Rule {
	return &Rule{Name: name, Steps: steps}
}

// TakeStep builds a take step.
func TakeStep(target any) Step {
	return Step{Op: OpTake, Take: target}
}

// ChooseStep builds a choose step. count zero means the mapping replica count.
func ChooseStep(mode Mode, count int, domain string, maxAttempts int) Step {
	return Step{Op: OpChoose, Mode: mode, Count: count, Type: domain, MaxAttempts: maxAttempts}
}

// ChooseLeafStep builds a chooseleaf step.
func ChooseLeafStep(mode Mode, count int, domain string, maxAttempts int) Step {
	return Step{Op: OpChooseLeaf, Mode: mode, Count: count, Type: domain, MaxAttempts: maxAttempts}
}

// EmitStep builds an emit step.
func EmitStep() Step {
	return Step{Op: OpEmit}
}

func (s Step) opValue() string {
	return normalizeOp(firstNonEmpty(s.Op, s.Operation, s.Action, s.Command))
}

func normalizeOp(op string) string {
	op = strings.ToLower(strings.TrimSpace(op))
	op = strings.ReplaceAll(op, "-", "_")
	op = strings.ReplaceAll(op, " ", "_")
	switch op {
	case "choose_leaf", "chooseleaf", "leaf", "choose_leaf_firstn", "chooseleaf_firstn", "choose_leaf_indep", "chooseleaf_indep":
		return OpChooseLeaf
	case "choose", "choose_firstn", "choose_indep":
		return OpChoose
	case "take", "root":
		return OpTake
	case "emit", "output":
		return OpEmit
	default:
		return op
	}
}

func (s Step) modeValue(fallback Mode) Mode {
	mode := strings.ToLower(strings.TrimSpace(firstNonEmpty(string(s.Mode), s.Strategy)))
	if mode == "" {
		rawOp := strings.ToLower(firstNonEmpty(s.Op, s.Operation, s.Action, s.Command))
		switch {
		case strings.Contains(rawOp, "indep") || strings.Contains(rawOp, "independent"):
			mode = string(Indep)
		case strings.Contains(rawOp, "firstn"):
			mode = string(FirstN)
		}
	}
	if mode == "" {
		mode = string(fallback)
	}
	switch mode {
	case "indep", "independent", "individual":
		return Indep
	case "firstn", "first_n", "first-n", "replicated", "":
		return FirstN
	default:
		return Mode(mode)
	}
}

func (s Step) domainValue() string {
	return firstNonEmpty(s.Type, s.BucketType, s.FailureDomain, s.Domain)
}

func (s Step) countValue(requested int) int {
	for _, n := range []int{s.Count, s.Number, s.N, s.Num, s.Replicas, s.Size} {
		if n != 0 {
			if n < 0 {
				if requested+n < 0 {
					return 0
				}
				return requested + n
			}
			return n
		}
	}
	return requested
}

func (s Step) maxAttemptsValue(def int) int {
	for _, n := range []int{s.MaxAttempts, s.MaxAttemptCount, s.Attempts, s.AttemptCount, s.Tries, s.MaxTries, s.TotalTries, s.ChooseTries} {
		if n > 0 {
			return n
		}
	}
	for _, n := range []int{s.MaxRetries, s.MaxRetryTimes, s.RetryTimes, s.RetryLimit, s.Retries, s.Retry, s.RetryCount} {
		if n > 0 {
			return n + 1
		}
	}
	return def
}

func (s Step) localAttemptsValue(def int) int {
	for _, n := range []int{s.LocalAttempts, s.LocalTries} {
		if n > 0 {
			return n
		}
	}
	return s.maxAttemptsValue(def)
}

func (s Step) targetValue() any {
	for _, target := range []any{s.Take, s.Item, s.Target, s.Root, s.ID, s.NodeID} {
		if target != nil {
			return target
		}
	}
	for _, id := range []int{s.TakeID, s.ItemID, s.TargetID, s.RootID, s.NodeIDV} {
		if id != 0 {
			return id
		}
	}
	return firstNonEmpty(s.TakeName, s.ItemName, s.RootName)
}
