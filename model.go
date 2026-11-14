package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Point is one observation in a trajectory. Temporal bucketing and time-of-day
// patterns use the time.Time's location; ordering and absolute-time quasi
// identifiers use the represented instant. Latitude and longitude are decimal
// degrees.
type Point struct {
	Time time.Time `json:"time"`
	Lat  float64   `json:"lat"`
	Lon  float64   `json:"lon"`
}

// Trajectory is a single mobility record.
//
// UserID and DirectIdentifiers are direct identifiers. They are never copied
// to an anonymized result and are excluded from JSON serialization. Quasi
// identifiers are published only after generalization and k-anonymity checks.
type Trajectory struct {
	ID                string            `json:"id,omitempty"`
	UserID            string            `json:"-"`
	DirectIdentifiers map[string]string `json:"-"`
	QuasiIdentifiers  map[string]string `json:"quasi_identifiers,omitempty"`
	Points            []Point           `json:"points"`
}

// Dataset is a collection of trajectories. It is a named slice so callers can
// pass either Dataset or []Trajectory to the component.
type Dataset []Trajectory

// Reserved quasi-identifier names derived from a trajectory's generalized
// shape. User-provided attributes cannot replace these values.
const (
	QIFrequentLocation1 = "frequent_location_1"
	QIFrequentLocation2 = "frequent_location_2"
	QIStartTimeOfDay    = "start_time_of_day"
	QIDayPattern        = "day_pattern"
	QIGeneralizedPath   = "generalized_path"
	QITemporalShape     = "temporal_shape"
)

var reservedQIs = map[string]struct{}{
	QIFrequentLocation1: {},
	QIFrequentLocation2: {},
	QIStartTimeOfDay:    {},
	QIDayPattern:        {},
	QIGeneralizedPath:   {},
	QITemporalShape:     {},
}

// Suppression explains why all or part of an input record was not published.
type Suppression struct {
	RecordID string `json:"record_id"`
	Stage    string `json:"stage"`
	Reason   string `json:"reason"`
	Details  string `json:"details,omitempty"`
}

// PublishReport contains utility and enforcement statistics for a run.
type PublishReport struct {
	InputRecords               int              `json:"input_records"`
	DirectIdentifiersRemoved   int              `json:"direct_identifiers_removed"`
	PublishedRecords           int              `json:"published_records"`
	SuppressedRecords          int              `json:"suppressed_records"`
	Suppressions               []Suppression    `json:"suppressions,omitempty"`
	SampledPointCount          int              `json:"sampled_point_count"`
	PublishedPointCount        int              `json:"published_point_count"`
	SpatialCellMeters          float64          `json:"spatial_cell_meters"`
	TemporalBucketSeconds      int64            `json:"temporal_bucket_seconds"`
	RareLocationPointsRemoved  int              `json:"rare_location_points_removed"`
	RareTrajectoriesSuppressed int              `json:"rare_trajectories_suppressed"`
	RegularPatternsSuppressed  int              `json:"regular_patterns_suppressed"`
	KAnonymitySuppressed       int              `json:"k_anonymity_suppressed"`
	KAnonymity                 KAnonymityReport `json:"k_anonymity"`
}

// Result is the output of the anonymization pipeline. Risk is assessed on the
// generalized, unperturbed records that are actually published.
type Result struct {
	Records []Trajectory  `json:"records"`
	Report  PublishReport `json:"report"`
	Risk    RiskReport    `json:"risk"`
}

// CanPublish reports whether the risk policy accepts this result.
func (r Result) CanPublish() bool {
	return r.Risk.Publishable && len(r.Records) > 0
}

// Published returns a defensive copy of the records when the risk policy
// accepts the result, and nil otherwise.
func (r Result) Published() []Trajectory {
	if !r.CanPublish() {
		return nil
	}
	return cloneTrajectories(r.Records)
}

// PublicationError is returned by Publish when the anonymized result does not
// satisfy the configured k-anonymity and re-identification risk policy.
type PublicationError struct {
	Risk    RiskReport
	Reasons []string
}

func (e *PublicationError) Error() string {
	if len(e.Reasons) == 0 {
		return fmt.Sprintf("publication blocked: risk score %.4f exceeds policy", e.Risk.RiskScore)
	}
	return fmt.Sprintf("publication blocked: %s", e.Reasons[0])
}

// Violation identifies an equivalence class that is smaller than k.
type Violation struct {
	Combination []string          `json:"combination"`
	Values      map[string]string `json:"values"`
	Size        int               `json:"size"`
}

// CombinationReport contains the k-anonymity result for one combination.
type CombinationReport struct {
	Combination  []string `json:"combination"`
	MinGroupSize int      `json:"min_group_size"`
	MaxGroupSize int      `json:"max_group_size"`
	GroupCount   int      `json:"group_count"`
	Satisfied    bool     `json:"satisfied"`
}

// KAnonymityReport is the result of checking every requested combination of
// quasi identifiers.
type KAnonymityReport struct {
	K                   int                 `json:"k"`
	Satisfied           bool                `json:"satisfied"`
	MinGroupSize        int                 `json:"min_group_size"`
	CheckedCombinations int                 `json:"checked_combinations"`
	Combinations        []CombinationReport `json:"combinations,omitempty"`
	Violations          []Violation         `json:"violations,omitempty"`
}

// RiskReport is a pre-publication re-identification assessment.
type RiskReport struct {
	Records                         int      `json:"records"`
	K                               int      `json:"k"`
	KAnonymitySatisfied             bool     `json:"k_anonymity_satisfied"`
	MinGroupSize                    int      `json:"min_group_size"`
	MaxReidentificationProbability  float64  `json:"max_reidentification_probability"`
	MeanReidentificationProbability float64  `json:"mean_reidentification_probability"`
	UniqueTrajectoryRate            float64  `json:"unique_trajectory_rate"`
	RareLocationRate                float64  `json:"rare_location_rate"`
	RareTrajectoryRate              float64  `json:"rare_trajectory_rate"`
	RegularPatternRate              float64  `json:"regular_pattern_rate"`
	RiskScore                       float64  `json:"risk_score"`
	RiskLevel                       string   `json:"risk_level"`
	Publishable                     bool     `json:"publishable"`
	Unlinkable                      bool     `json:"unlinkable"`
	Violations                      []string `json:"violations,omitempty"`
	Recommendations                 []string `json:"recommendations,omitempty"`
}

func cloneTrajectories(in []Trajectory) []Trajectory {
	out := make([]Trajectory, len(in))
	for i := range in {
		out[i] = cloneTrajectory(in[i])
	}
	return out
}

func cloneTrajectory(in Trajectory) Trajectory {
	out := in
	if in.Points != nil {
		out.Points = append([]Point(nil), in.Points...)
	}
	out.DirectIdentifiers = cloneMap(in.DirectIdentifiers)
	out.QuasiIdentifiers = cloneMap(in.QuasiIdentifiers)
	return out
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func hashToken(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return "tr-" + hex.EncodeToString(h.Sum(nil))[:16]
}
