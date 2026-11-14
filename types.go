package netsec

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Statistic identifies the aggregate requested by a query.
type Statistic string

const (
	StatisticCount Statistic = "count"
	StatisticSum   Statistic = "sum"
	StatisticMean  Statistic = "mean"
)

func (s Statistic) valid() bool {
	switch s {
	case StatisticCount, StatisticSum, StatisticMean:
		return true
	default:
		return false
	}
}

// GroupSummary is one group's aggregate as supplied by the statistics
// service. Subjects and SubjectIDs are aliases; at least one must contain
// the identifiers when per-subject accounting is required.
type GroupSummary struct {
	Key        string   `json:"key"`
	Subjects   []string `json:"subjects,omitempty"`
	SubjectIDs []string `json:"subject_ids,omitempty"`

	// Count is used only when no subject identifiers are supplied, and only
	// if Policy.AllowUnlinkedCells is true.
	Count float64 `json:"count,omitempty"`

	// Value is the precomputed aggregate value from the statistics service.
	// It is required for sum and mean queries; count queries use Count (or
	// the subject set).
	Value float64 `json:"value,omitempty"`

	// Sensitive marks this cell as involving sensitive data.
	Sensitive bool `json:"sensitive,omitempty"`

	// SensitiveCount is the number of sensitive records in the group. If it
	// is positive and below SensitiveMinGroupSize the cell is suppressed.
	SensitiveCount int `json:"sensitive_count,omitempty"`
}

// Query asks the disclosure-control engine to check and release a grouped
// aggregate. The service supplies already grouped summaries; the engine does
// not expose raw records.
type Query struct {
	ID        string         `json:"id"`
	Statistic Statistic      `json:"statistic"`
	Attribute string         `json:"attribute,omitempty"`
	Groups    []GroupSummary `json:"groups"`

	// Epsilon is the privacy cost. Zero selects Policy.DefaultEpsilon.
	Epsilon float64 `json:"epsilon,omitempty"`

	// Sensitivity is the per-cell sensitivity used for noise calibration.
	// Zero defaults to 1. Sum callers must supply the maximum absolute
	// contribution of one subject; ExecuteDataset derives this automatically.
	Sensitivity float64 `json:"sensitivity,omitempty"`

	// Sensitive forces sensitive handling even when Attribute is not in
	// Policy.SensitiveAttributes.
	Sensitive bool `json:"sensitive,omitempty"`
}

// SuppressionReason explains why a group was not released.
type SuppressionReason string

const (
	ReasonMinimumGroupSize   SuppressionReason = "minimum_group_size"
	ReasonSensitiveAttribute SuppressionReason = "sensitive_attribute"
	ReasonSensitiveSubgroup  SuppressionReason = "sensitive_subgroup"
	ReasonNoSubjectIdentity  SuppressionReason = "no_subject_identity"
)

// GroupResult is the disclosure-controlled output for one group.
type GroupResult struct {
	Key          string              `json:"key"`
	Released     bool                `json:"released"`
	Value        float64             `json:"value"`
	NoiseApplied bool                `json:"noise_applied,omitempty"`
	Rounded      bool                `json:"rounded,omitempty"`
	Reasons      []SuppressionReason `json:"reasons,omitempty"`
}

// Result is the outcome of an accepted query. A query can be accepted even
// when every group is suppressed; such a query still consumes budget because
// the suppression signal itself is protected output.
type Result struct {
	QueryID         string        `json:"query_id"`
	Statistic       Statistic     `json:"statistic"`
	Attribute       string        `json:"attribute,omitempty"`
	Groups          []GroupResult `json:"groups"`
	EpsilonSpent    float64       `json:"epsilon_spent"`
	BudgetSpent     float64       `json:"budget_spent"`
	BudgetRemaining float64       `json:"budget_remaining"`
	AnyReleased     bool          `json:"any_released"`
	// UnlinkedGroups counts groups that had no subject identifiers and
	// therefore could not participate in per-subject accounting.
	UnlinkedGroups int `json:"unlinked_groups,omitempty"`
}

// BudgetStatus is a point-in-time view of the engine's sequential-composition
// ledger. It is safe to call concurrently with Execute.
type BudgetStatus struct {
	TotalBudget         float64 `json:"total_budget"`
	BudgetSpent         float64 `json:"budget_spent"`
	BudgetRemaining     float64 `json:"budget_remaining"`
	SubjectBudget       float64 `json:"subject_budget"`
	SubjectsTracked     int     `json:"subjects_tracked"`
	QueriesAccepted     int     `json:"queries_accepted"`
	MinSubjectRemaining float64 `json:"min_subject_remaining"`
}

var (
	ErrInvalidQuery         = errors.New("netsec: invalid query")
	ErrDuplicateQuery       = errors.New("netsec: duplicate query")
	ErrCrossQueryRestricted = errors.New("netsec: cross-query restriction")
	ErrBudgetExhausted      = errors.New("netsec: budget exhausted")
	ErrSubjectQueryLimit    = errors.New("netsec: subject query limit")
	ErrUnlinkedCell         = errors.New("netsec: cell has no subject identifiers")
)

func invalidQueryf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidQuery, fmt.Sprintf(format, args...))
}

func normalizeQuery(q Query, policy Policy) (Query, []string, [][]string, error) {
	if q.Statistic == "" {
		q.Statistic = StatisticCount
	}
	if !q.Statistic.valid() {
		return Query{}, nil, nil, invalidQueryf("unknown statistic %q", q.Statistic)
	}
	if (q.Statistic == StatisticSum || q.Statistic == StatisticMean) && strings.TrimSpace(q.Attribute) == "" {
		return Query{}, nil, nil, invalidQueryf("%s requires an attribute", q.Statistic)
	}
	if len(q.Groups) == 0 {
		return Query{}, nil, nil, invalidQueryf("query has no groups")
	}
	if q.Epsilon == 0 {
		q.Epsilon = policy.DefaultEpsilon
	}
	if q.Epsilon < 0 || math.IsNaN(q.Epsilon) || math.IsInf(q.Epsilon, 0) {
		return Query{}, nil, nil, invalidQueryf("epsilon must be positive")
	}
	if q.Sensitivity == 0 {
		q.Sensitivity = 1
	}
	if q.Sensitivity < 0 || math.IsNaN(q.Sensitivity) || math.IsInf(q.Sensitivity, 0) {
		return Query{}, nil, nil, invalidQueryf("sensitivity must be positive")
	}

	seenKeys := make(map[string]struct{}, len(q.Groups))
	keys := make([]string, 0, len(q.Groups))
	subjectsPerGroup := make([][]string, 0, len(q.Groups))
	for i, group := range q.Groups {
		if strings.TrimSpace(group.Key) == "" {
			return Query{}, nil, nil, invalidQueryf("group %d has an empty key", i)
		}
		if _, exists := seenKeys[group.Key]; exists {
			return Query{}, nil, nil, invalidQueryf("duplicate group key %q", group.Key)
		}
		seenKeys[group.Key] = struct{}{}

		subjects := uniqueSubjects(group.Subjects, group.SubjectIDs)
		if group.Count < 0 || math.IsNaN(group.Count) || math.IsInf(group.Count, 0) {
			return Query{}, nil, nil, invalidQueryf("group %q has an invalid count", group.Key)
		}
		if group.SensitiveCount < 0 {
			return Query{}, nil, nil, invalidQueryf("group %q has a negative sensitive count", group.Key)
		}
		if len(subjects) == 0 {
			if !policy.AllowUnlinkedCells {
				return Query{}, nil, nil, fmt.Errorf("%w: group %q", ErrUnlinkedCell, group.Key)
			}
		}
		if math.IsNaN(group.Value) || math.IsInf(group.Value, 0) {
			return Query{}, nil, nil, invalidQueryf("group %q has a non-finite value", group.Key)
		}
		keys = append(keys, group.Key)
		subjectsPerGroup = append(subjectsPerGroup, subjects)
	}
	return q, keys, subjectsPerGroup, nil
}

func uniqueSubjects(subjects, subjectIDs []string) []string {
	set := make(map[string]struct{}, len(subjects)+len(subjectIDs))
	for _, subject := range subjects {
		if subject = strings.TrimSpace(subject); subject != "" {
			set[subject] = struct{}{}
		}
	}
	for _, subject := range subjectIDs {
		if subject = strings.TrimSpace(subject); subject != "" {
			set[subject] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for subject := range set {
		out = append(out, subject)
	}
	sort.Strings(out)
	return out
}

func groupSubjectCount(group GroupSummary, subjects []string) int {
	if len(subjects) > 0 {
		return len(subjects)
	}
	return int(group.Count)
}
