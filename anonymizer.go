package netsec

import (
	"errors"
	"fmt"
	"time"
)

// ErrEmptyDataset is returned when there is nothing to anonymize or assess.
var ErrEmptyDataset = errors.New("netsec: empty dataset")

// Anonymizer is a reusable, concurrency-safe anonymization component. It
// stores immutable configuration and does not retain caller data.
type Anonymizer struct {
	cfg Config
}

// NewAnonymizer validates and normalizes configuration.
func NewAnonymizer(cfg Config) (*Anonymizer, error) {
	normalized, err := NormalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Anonymizer{cfg: normalized}, nil
}

// New is an alias for NewAnonymizer.
func New(cfg Config) (*Anonymizer, error) {
	return NewAnonymizer(cfg)
}

// Config returns the normalized configuration.
func (a *Anonymizer) Config() Config {
	out := a.cfg
	out.QICombinations = cloneCombinations(a.cfg.QICombinations)
	return out
}

// Anonymize runs the full pipeline:
//
//  1. remove direct identifiers and invalid points;
//  2. sample and spatially/temporally generalize points;
//  3. suppress rare locations, rare generalized paths and rare regular
//     temporal patterns;
//  4. enforce k-anonymity for every quasi-identifier combination; and
//  5. assess re-identification risk and mark the result publishable or not.
func (a *Anonymizer) Anonymize(ds Dataset) (*Result, error) {
	if len(ds) == 0 {
		return nil, ErrEmptyDataset
	}
	if err := validateQICombinationNames(ds, a.cfg); err != nil {
		return nil, err
	}

	records := prepareRecords(ds, a.cfg)
	report := PublishReport{
		InputRecords:          len(ds),
		SpatialCellMeters:     a.cfg.SpatialCellMeters,
		TemporalBucketSeconds: int64(a.cfg.TimeBucket / time.Second),
	}
	for _, rec := range ds {
		if rec.UserID != "" {
			report.DirectIdentifiersRemoved++
		}
		report.DirectIdentifiersRemoved += len(rec.DirectIdentifiers)
	}
	for _, rec := range records {
		report.SampledPointCount += rec.sampledPoints
	}

	for iteration := 0; iteration < a.cfg.MaxIterations; iteration++ {
		changed := false
		if removeRareLocations(records, a.cfg, &report) {
			changed = true
		}
		if removeRareTrajectories(records, a.cfg, &report) {
			changed = true
		}
		if removeRareRegularPatterns(records, a.cfg, &report) {
			changed = true
		}
		_, kChanged, err := enforceKAnonymity(records, a.cfg, &report)
		if err != nil {
			return nil, err
		}
		if kChanged {
			changed = true
		}
		if !changed {
			break
		}
	}

	finalK, _, err := checkKAnonymity(records, a.cfg)
	if err != nil {
		return nil, err
	}
	report.KAnonymity = finalK
	risk := analyzeRisk(records, a.cfg)
	if !risk.Publishable && risk.Records > 0 {
		// Give the caller a precise recommendation even if the loop hit its
		// iteration bound.
		risk.Recommendations = append(risk.Recommendations,
			fmt.Sprintf("anonymization converged with %d published records", risk.Records))
	}

	for _, rec := range records {
		if !rec.active {
			report.Suppressions = append(report.Suppressions, rec.suppressions...)
		}
	}

	out := make([]Trajectory, 0, len(records))
	for _, rec := range records {
		if !rec.active {
			continue
		}
		applyPerturbation(rec, a.cfg)
		report.PublishedPointCount += len(rec.points)
		out = append(out, Trajectory{
			ID:               rec.outputID,
			QuasiIdentifiers: cloneMap(rec.quasi),
			Points:           append([]Point(nil), rec.points...),
		})
	}

	report.PublishedRecords = len(out)
	report.SuppressedRecords = report.InputRecords - report.PublishedRecords
	return &Result{Records: out, Report: report, Risk: risk}, nil
}

// Publish is Anonymize plus a policy gate. It returns the result together with
// a *PublicationError when the configured risk threshold is not met.
func (a *Anonymizer) Publish(ds Dataset) (*Result, error) {
	result, err := a.Anonymize(ds)
	if err != nil {
		return nil, err
	}
	if !result.CanPublish() {
		reasons := append([]string(nil), result.Risk.Violations...)
		if len(reasons) == 0 {
			reasons = []string{fmt.Sprintf("risk score %.4f exceeds policy %.4f", result.Risk.RiskScore, a.cfg.MaxRisk)}
		}
		return result, &PublicationError{Risk: result.Risk, Reasons: reasons}
	}
	return result, nil
}

// Anonymize is a package-level convenience wrapper.
func Anonymize(ds Dataset, cfg Config) (*Result, error) {
	a, err := NewAnonymizer(cfg)
	if err != nil {
		return nil, err
	}
	return a.Anonymize(ds)
}

// Publish is a package-level convenience wrapper that enforces the risk policy.
func Publish(ds Dataset, cfg Config) (*Result, error) {
	a, err := NewAnonymizer(cfg)
	if err != nil {
		return nil, err
	}
	return a.Publish(ds)
}
