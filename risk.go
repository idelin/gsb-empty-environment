package netsec

import (
	"fmt"
	"sort"
	"strings"
)

func analyzeRisk(recs []*workRecord, cfg Config) RiskReport {
	active := activeRecords(recs)
	report := RiskReport{
		Records: len(active),
		K:       cfg.K,
	}
	if len(active) == 0 {
		report.RiskScore = 1
		report.RiskLevel = "high"
		report.Publishable = false
		report.Unlinkable = false
		report.Violations = []string{"no records remain after anonymization"}
		report.Recommendations = []string{"increase spatial/temporal generalization or lower k for this dataset"}
		return report
	}

	cellSupport := cellSupports(active)
	totalPoints := 0
	rarePoints := 0
	for _, rec := range active {
		totalPoints += len(rec.points)
		seen := make(map[string]struct{}, len(rec.cells))
		for _, cell := range rec.cells {
			seen[cell.ID] = struct{}{}
		}
		for id := range seen {
			if cellSupport[id] < cfg.MinLocationSupport {
				rarePoints += countCell(rec.cells, id)
			}
		}
	}
	if totalPoints > 0 {
		report.RareLocationRate = float64(rarePoints) / float64(totalPoints)
	}

	pathSupport := pathSupports(active)
	rareTrajectories := 0
	for _, rec := range active {
		if pathSupport[generalizedPath(rec.cells)] < cfg.MinTrajectorySupport {
			rareTrajectories++
		}
	}
	report.RareTrajectoryRate = float64(rareTrajectories) / float64(len(active))

	patternSupport := make(map[string]int)
	regular := make(map[*workRecord]bool)
	for _, rec := range active {
		if key, ok := regularPattern(rec, cfg); ok {
			regular[rec] = true
			patternSupport[key]++
		}
	}
	rareRegular := 0
	for _, rec := range active {
		if !regular[rec] {
			continue
		}
		key, _ := regularPattern(rec, cfg)
		if patternSupport[key] < cfg.MinPatternSupport {
			rareRegular++
		}
	}
	report.RegularPatternRate = float64(rareRegular) / float64(len(active))

	combos, err := combinationsFor(active, cfg)
	if err != nil {
		report.RiskScore = 1
		report.RiskLevel = "high"
		report.Violations = []string{err.Error()}
		return report
	}

	values := make(map[*workRecord]map[string]string, len(active))
	for _, rec := range active {
		values[rec] = deriveQIValues(rec, cfg)
	}

	fullCombo := availableQIs(active)
	riskCombos := append([][]string(nil), combos...)
	hasFull := false
	fullKey := comboNameKey(fullCombo)
	for _, combo := range combos {
		if comboNameKey(combo) == fullKey {
			hasFull = true
			break
		}
	}
	if !hasFull {
		riskCombos = append(riskCombos, fullCombo)
	}

	maxProbability := 0.0
	meanProbability := 0.0
	checked := 0
	for _, combo := range riskCombos {
		groups := make(map[string][]*workRecord)
		for _, rec := range active {
			key := comboKey(values[rec], combo)
			groups[key] = append(groups[key], rec)
		}
		for _, members := range groups {
			probability := 1 / float64(len(members))
			if probability > maxProbability {
				maxProbability = probability
			}
			for range members {
				meanProbability += probability
				checked++
			}
		}
	}

	fullGroups := make(map[string]int)
	for _, rec := range active {
		fullGroups[comboKey(values[rec], fullCombo)]++
	}
	unique := 0
	fullMin := len(active)
	for rec := range values {
		size := fullGroups[comboKey(values[rec], fullCombo)]
		if size < fullMin {
			fullMin = size
		}
	}
	for _, rec := range active {
		if fullGroups[comboKey(values[rec], fullCombo)] == 1 {
			unique++
		}
	}
	report.UniqueTrajectoryRate = float64(unique) / float64(len(active))
	if checked > 0 {
		report.MeanReidentificationProbability = meanProbability / float64(checked)
	}
	report.MaxReidentificationProbability = maxProbability

	kReport, _, err := checkKAnonymity(active, cfg)
	if err != nil {
		report.Violations = append(report.Violations, err.Error())
	}
	report.MinGroupSize = min(kReport.MinGroupSize, fullMin)
	report.KAnonymitySatisfied = kReport.Satisfied && fullMin >= cfg.K

	report.RiskScore = maxProbability
	report.RiskScore = max(report.RiskScore, report.RareLocationRate)
	report.RiskScore = max(report.RiskScore, report.RareTrajectoryRate)
	report.RiskScore = max(report.RiskScore, report.RegularPatternRate)

	if report.MinGroupSize < cfg.K {
		report.Violations = append(report.Violations,
			fmt.Sprintf("minimum quasi-identifier group size %d is below k=%d", report.MinGroupSize, cfg.K))
	}
	if fullMin < cfg.K {
		report.Violations = append(report.Violations,
			fmt.Sprintf("full quasi-identifier combination has a group of %d, below k=%d", fullMin, cfg.K))
	}
	if report.MaxReidentificationProbability > cfg.MaxRisk {
		report.Violations = append(report.Violations,
			fmt.Sprintf("maximum re-identification probability %.4f exceeds %.4f",
				report.MaxReidentificationProbability, cfg.MaxRisk))
	}
	if report.RareLocationRate > 0 {
		report.Violations = append(report.Violations,
			fmt.Sprintf("%.2f%% of points remain in rare locations", report.RareLocationRate*100))
	}
	if report.RareTrajectoryRate > 0 {
		report.Violations = append(report.Violations,
			fmt.Sprintf("%.2f%% of trajectories have rare generalized paths", report.RareTrajectoryRate*100))
	}
	if report.RegularPatternRate > 0 {
		report.Violations = append(report.Violations,
			fmt.Sprintf("%.2f%% of trajectories have rare regular temporal patterns", report.RegularPatternRate*100))
	}

	report.Publishable = report.Records > 0 &&
		report.KAnonymitySatisfied &&
		report.MinGroupSize >= cfg.K &&
		report.RareLocationRate == 0 &&
		report.RareTrajectoryRate == 0 &&
		report.RegularPatternRate == 0 &&
		report.MaxReidentificationProbability <= cfg.MaxRisk+1e-9 &&
		report.RiskScore <= cfg.MaxRisk+1e-9
	report.Unlinkable = report.Publishable

	switch {
	case report.Publishable:
		report.RiskLevel = "low"
	case report.RiskScore <= 0.5:
		report.RiskLevel = "medium"
	default:
		report.RiskLevel = "high"
	}

	if report.MinGroupSize < cfg.K {
		report.Recommendations = append(report.Recommendations,
			"increase spatial cell size, time bucket size, or add records sharing quasi-identifiers")
	}
	if report.MaxReidentificationProbability > cfg.MaxRisk {
		report.Recommendations = append(report.Recommendations,
			"increase the effective group size or review the MaxRisk policy")
	}
	if report.RareLocationRate > 0 {
		report.Recommendations = append(report.Recommendations,
			"increase MinLocationSupport or merge nearby locations")
	}
	if report.RareTrajectoryRate > 0 {
		report.Recommendations = append(report.Recommendations,
			"increase generalization of path shapes or suppress rare paths")
	}
	if report.RegularPatternRate > 0 {
		report.Recommendations = append(report.Recommendations,
			"coarsen time buckets or suppress rare recurring routines")
	}
	sort.Strings(report.Violations)
	return report
}

func comboNameKey(combo []string) string {
	return strings.Join(combo, "\x00")
}

func countCell(cells []cellBox, id string) int {
	n := 0
	for _, cell := range cells {
		if cell.ID == id {
			n++
		}
	}
	return n
}

// AssessRisk performs a pre-publication re-identification assessment without
// suppressing records. Spatial and temporal generalization is applied so risk
// reflects what an attacker could infer from a published, generalized dataset.
func AssessRisk(ds Dataset, cfg Config) (RiskReport, error) {
	a, err := NewAnonymizer(cfg)
	if err != nil {
		return RiskReport{}, err
	}
	if len(ds) == 0 {
		return RiskReport{}, ErrEmptyDataset
	}
	if err := validateQICombinationNames(ds, a.cfg); err != nil {
		return RiskReport{}, err
	}
	prepared := prepareRecords(ds, a.cfg)
	return analyzeRisk(prepared, a.cfg), nil
}

// ValidateKAnonymity checks all selected quasi-identifier combinations on a
// generalized dataset. It reports violations but does not suppress records.
func ValidateKAnonymity(ds Dataset, cfg Config) (KAnonymityReport, error) {
	a, err := NewAnonymizer(cfg)
	if err != nil {
		return KAnonymityReport{}, err
	}
	if len(ds) == 0 {
		return KAnonymityReport{}, ErrEmptyDataset
	}
	if err := validateQICombinationNames(ds, a.cfg); err != nil {
		return KAnonymityReport{}, err
	}
	prepared := prepareRecords(ds, a.cfg)
	report, _, err := checkKAnonymity(prepared, a.cfg)
	return report, err
}

// CheckKAnonymity is an alias for ValidateKAnonymity.
func CheckKAnonymity(ds Dataset, cfg Config) (KAnonymityReport, error) {
	return ValidateKAnonymity(ds, cfg)
}
