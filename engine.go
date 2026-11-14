package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	mathrand "math/rand/v2"
	"sort"
	"sync"
)

const budgetEpsilon = 1e-9

type seenQuery struct {
	subjects map[string]struct{}
}

// Engine holds disclosure policy and the query ledger. It is safe for
// concurrent use.
type Engine struct {
	mu      sync.Mutex
	policy  Policy
	dataset *Dataset
	rng     *mathrand.Rand

	spent           float64
	subjectSpent    map[string]float64
	subjectQueries  map[string]int
	seen            map[string]seenQuery
	queriesAccepted int
}

// NewEngine constructs an engine. The dataset is optional; zero or one may be
// supplied. Without one, queries must provide subject-aware GroupSummary cells.
func NewEngine(policy Policy, datasets ...*Dataset) (*Engine, error) {
	normalized, err := policy.Normalize()
	if err != nil {
		return nil, err
	}
	if len(datasets) > 1 {
		return nil, fmt.Errorf("netsec: at most one dataset may be supplied")
	}
	var dataset *Dataset
	if len(datasets) == 1 {
		dataset = datasets[0]
	}
	rng, err := newRand(normalized.RandomSeed)
	if err != nil {
		return nil, fmt.Errorf("netsec: initialize noise source: %w", err)
	}
	return &Engine{
		policy:         normalized,
		dataset:        dataset,
		rng:            rng,
		subjectSpent:   make(map[string]float64),
		subjectQueries: make(map[string]int),
		seen:           make(map[string]seenQuery),
	}, nil
}

// Policy returns the normalized policy in use.
func (e *Engine) Policy() Policy {
	e.mu.Lock()
	defer e.mu.Unlock()
	policy := e.policy
	policy.SensitiveAttributes = cloneBoolMap(e.policy.SensitiveAttributes)
	return policy
}

// Budget returns a snapshot of global and aggregate per-subject budget use.
func (e *Engine) Budget() BudgetStatus {
	e.mu.Lock()
	defer e.mu.Unlock()

	status := BudgetStatus{
		TotalBudget:         e.policy.TotalBudget,
		BudgetSpent:         e.spent,
		BudgetRemaining:     math.Max(0, e.policy.TotalBudget-e.spent),
		SubjectBudget:       e.policy.SubjectBudget,
		SubjectsTracked:     len(e.subjectSpent),
		QueriesAccepted:     e.queriesAccepted,
		MinSubjectRemaining: e.policy.SubjectBudget,
	}
	for subject := range e.subjectSpent {
		remaining := e.policy.SubjectBudget - e.subjectSpent[subject]
		if remaining < status.MinSubjectRemaining {
			status.MinSubjectRemaining = remaining
		}
	}
	if status.MinSubjectRemaining < 0 {
		status.MinSubjectRemaining = 0
	}
	return status
}

// SubjectStatus returns how much budget and how many accepted queries a
// subject has accumulated.
func (e *Engine) SubjectStatus(subjectID string) (spent float64, queries int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.subjectSpent[subjectID], e.subjectQueries[subjectID]
}

// Execute validates, accounts for, and releases (or suppresses) a grouped
// query. Errors mean nothing was released and no budget was consumed.
func (e *Engine) Execute(query Query) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	normalized, keys, subjectsPerGroup, err := normalizeQuery(query, e.policy)
	if err != nil {
		return Result{}, err
	}

	fingerprint, err := queryFingerprint(normalized, keys)
	if err != nil {
		return Result{}, err
	}
	if normalized.ID == "" {
		normalized.ID = "q_" + fingerprint[:16]
	}

	if _, duplicate := e.seen[fingerprint]; duplicate {
		return Result{}, fmt.Errorf("%w: query %q repeats an accepted group/metric", ErrDuplicateQuery, normalized.ID)
	}

	subjectSet := unionSubjects(subjectsPerGroup)
	if err := e.checkOverlap(normalized.ID, subjectSet); err != nil {
		return Result{}, err
	}
	if err := e.checkBudget(normalized.Epsilon, subjectSet); err != nil {
		return Result{}, err
	}

	e.charge(normalized.Epsilon, subjectSet)
	e.seen[fingerprint] = seenQuery{
		subjects: subjectSet,
	}
	e.queriesAccepted++

	result := Result{
		QueryID:         normalized.ID,
		Statistic:       normalized.Statistic,
		Attribute:       normalized.Attribute,
		Groups:          make([]GroupResult, 0, len(normalized.Groups)),
		EpsilonSpent:    normalized.Epsilon,
		BudgetSpent:     e.spent,
		BudgetRemaining: math.Max(0, e.policy.TotalBudget-e.spent),
	}

	sensitiveQuery := normalized.Sensitive || e.policy.isSensitiveAttribute(normalized.Attribute)
	maxParticipation := maxCellParticipation(subjectsPerGroup)
	if maxParticipation == 0 {
		maxParticipation = len(normalized.Groups)
	}
	for i, group := range normalized.Groups {
		subjects := subjectsPerGroup[i]
		count := groupSubjectCount(group, subjects)
		if len(subjects) == 0 {
			result.UnlinkedGroups++
		}

		reasons := make([]SuppressionReason, 0, 3)
		if count < e.policy.MinGroupSize {
			reasons = append(reasons, ReasonMinimumGroupSize)
		}
		if len(subjects) == 0 && group.Count <= 0 {
			reasons = append(reasons, ReasonNoSubjectIdentity)
		}

		cellSensitive := sensitiveQuery || group.Sensitive
		if cellSensitive {
			switch {
			case e.policy.SensitiveMode == SensitiveSuppressAll && sensitiveQuery:
				reasons = append(reasons, ReasonSensitiveAttribute)
			case e.policy.SensitiveMode == SensitiveSuppressAll:
				reasons = append(reasons, ReasonSensitiveSubgroup)
			case count < e.policy.SensitiveMinGroupSize:
				if sensitiveQuery {
					reasons = append(reasons, ReasonSensitiveAttribute)
				} else {
					reasons = append(reasons, ReasonSensitiveSubgroup)
				}
			}
		}
		if group.SensitiveCount > 0 && group.SensitiveCount < e.policy.SensitiveMinGroupSize {
			reasons = appendUniqueReason(reasons, ReasonSensitiveSubgroup)
		}

		groupResult := GroupResult{Key: group.Key}
		if len(reasons) > 0 {
			groupResult.Reasons = reasons
			result.Groups = append(result.Groups, groupResult)
			continue
		}

		value := group.Value
		if normalized.Statistic == StatisticCount {
			value = float64(count)
		}
		if e.policy.NoiseMode == NoiseLaplace {
			scale := normalized.Sensitivity * float64(maxParticipation) / normalized.Epsilon
			value += laplaceSample(e.rng, scale)
			groupResult.NoiseApplied = true
		}
		if normalized.Statistic == StatisticCount || normalized.Statistic == StatisticMean {
			if value < 0 {
				value = 0
			}
		}
		if e.policy.RoundingBase > 1 {
			value = roundTo(value, e.policy.RoundingBase)
			groupResult.Rounded = true
		}
		groupResult.Released = true
		groupResult.Value = value
		result.AnyReleased = true
		result.Groups = append(result.Groups, groupResult)
	}

	return result, nil
}

// ExecuteDataset builds summaries from the engine's dataset and executes them.
// It is a convenience for services that retain record-level data.
func (e *Engine) ExecuteDataset(groups []string, statistic Statistic, attribute string, epsilon float64) (Result, error) {
	if e.dataset == nil {
		return Result{}, fmt.Errorf("%w: engine has no dataset", ErrInvalidQuery)
	}
	query := Query{
		Statistic: statistic,
		Attribute: attribute,
		Epsilon:   epsilon,
		Groups:    make([]GroupSummary, 0, len(groups)),
	}
	if statistic == StatisticSum || statistic == StatisticMean {
		query.Sensitivity = e.dataset.MaxAbsValue(attribute)
	}
	for _, group := range groups {
		summary, err := e.dataset.Summary(group, attribute, statistic)
		if err != nil {
			return Result{}, fmt.Errorf("%w: %v", ErrInvalidQuery, err)
		}
		query.Groups = append(query.Groups, summary)
	}
	return e.Execute(query)
}

func (e *Engine) checkOverlap(queryID string, subjects map[string]struct{}) error {
	if e.policy.MaxQueryOverlap < 0 || len(subjects) == 0 {
		return nil
	}
	for _, previous := range e.seen {
		smaller := len(subjects)
		if len(previous.subjects) < smaller {
			smaller = len(previous.subjects)
		}
		if smaller == 0 {
			continue
		}
		overlap := 0
		for subject := range subjects {
			if _, exists := previous.subjects[subject]; exists {
				overlap++
			}
		}
		coefficient := float64(overlap) / float64(smaller)
		if coefficient >= e.policy.MaxQueryOverlap {
			return fmt.Errorf("%w: query %q overlaps an accepted query on %.2f of the smaller subject set (limit %.2f)",
				ErrCrossQueryRestricted, queryID, coefficient, e.policy.MaxQueryOverlap)
		}
	}
	return nil
}

func (e *Engine) checkBudget(epsilon float64, subjects map[string]struct{}) error {
	if e.spent+epsilon > e.policy.TotalBudget+budgetEpsilon {
		return fmt.Errorf("%w: global spent %.4f + %.4f exceeds %.4f",
			ErrBudgetExhausted, e.spent, epsilon, e.policy.TotalBudget)
	}
	for subject := range subjects {
		if e.subjectSpent[subject]+epsilon > e.policy.SubjectBudget+budgetEpsilon {
			return fmt.Errorf("%w: subject budget %.4f would be exceeded",
				ErrBudgetExhausted, e.policy.SubjectBudget)
		}
		if e.subjectQueries[subject] >= e.policy.MaxQueriesPerSubject {
			return fmt.Errorf("%w: subject reached %d accepted queries",
				ErrSubjectQueryLimit, e.policy.MaxQueriesPerSubject)
		}
	}
	return nil
}

func (e *Engine) charge(epsilon float64, subjects map[string]struct{}) {
	e.spent += epsilon
	for subject := range subjects {
		e.subjectSpent[subject] += epsilon
		e.subjectQueries[subject]++
	}
}

func queryFingerprint(query Query, keys []string) (string, error) {
	sortedKeys := append([]string(nil), keys...)
	sort.Strings(sortedKeys)
	payload := struct {
		Statistic Statistic `json:"statistic"`
		Attribute string    `json:"attribute"`
		Keys      []string  `json:"keys"`
	}{
		Statistic: query.Statistic,
		Attribute: query.Attribute,
		Keys:      sortedKeys,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func unionSubjects(groups [][]string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, subjects := range groups {
		for _, subject := range subjects {
			set[subject] = struct{}{}
		}
	}
	return set
}

func maxCellParticipation(groups [][]string) int {
	participation := make(map[string]int)
	maximum := 0
	for _, subjects := range groups {
		for _, subject := range subjects {
			participation[subject]++
			if participation[subject] > maximum {
				maximum = participation[subject]
			}
		}
	}
	return maximum
}

func appendUniqueReason(reasons []SuppressionReason, reason SuppressionReason) []SuppressionReason {
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}
