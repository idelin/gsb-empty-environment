package netsec

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Record is one subject's contribution to one group. A subject may appear in
// multiple groups, but may appear at most once in the same group.
type Record struct {
	SubjectID string             `json:"subject_id"`
	Group     string             `json:"group"`
	Values    map[string]float64 `json:"values,omitempty"`
	Sensitive bool               `json:"sensitive,omitempty"`
}

// Dataset is an immutable record-level view used to build grouped summaries.
// The disclosure engine only needs summaries, but retaining records lets a Go
// service translate its warehouse rows into subject-aware queries.
type Dataset struct {
	groups map[string]map[string]Record
}

// NewDataset validates and indexes records. Subject identifiers and group keys
// are required. A duplicate (subject, group) pair is rejected because it would
// make per-subject accounting ambiguous.
func NewDataset(records []Record) (*Dataset, error) {
	dataset := &Dataset{groups: make(map[string]map[string]Record)}
	for i, record := range records {
		if strings.TrimSpace(record.SubjectID) == "" {
			return nil, fmt.Errorf("record %d: empty subject ID", i)
		}
		if strings.TrimSpace(record.Group) == "" {
			return nil, fmt.Errorf("record %d: empty group", i)
		}
		group, exists := dataset.groups[record.Group]
		if !exists {
			group = make(map[string]Record)
			dataset.groups[record.Group] = group
		}
		if _, exists := group[record.SubjectID]; exists {
			return nil, fmt.Errorf("record %d: duplicate subject %q in group %q", i, record.SubjectID, record.Group)
		}
		values := make(map[string]float64, len(record.Values))
		for attribute, value := range record.Values {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, fmt.Errorf("record %d: attribute %q is not finite", i, attribute)
			}
			values[attribute] = value
		}
		record.Values = values
		group[record.SubjectID] = record
	}
	return dataset, nil
}

// Groups returns all group keys in sorted order.
func (d *Dataset) Groups() []string {
	groups := make([]string, 0, len(d.groups))
	for group := range d.groups {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return groups
}

// Subjects returns the distinct subject identifiers in a group, sorted.
func (d *Dataset) Subjects(group string) []string {
	records := d.groups[group]
	subjects := make([]string, 0, len(records))
	for subject := range records {
		subjects = append(subjects, subject)
	}
	sort.Strings(subjects)
	return subjects
}

// Summary builds a GroupSummary for one group.
func (d *Dataset) Summary(group string, attribute string, statistic Statistic) (GroupSummary, error) {
	records, exists := d.groups[group]
	if !exists {
		return GroupSummary{}, fmt.Errorf("dataset: unknown group %q", group)
	}

	subjects := make([]string, 0, len(records))
	for subject := range records {
		subjects = append(subjects, subject)
	}
	sort.Strings(subjects)

	count := float64(len(subjects))
	value := count
	sensitiveCount := 0
	sensitive := false
	sum := 0.0
	if statistic != StatisticCount {
		for _, subject := range subjects {
			record := records[subject]
			sum += record.Values[attribute]
		}
		switch statistic {
		case StatisticSum:
			value = sum
		case StatisticMean:
			if len(subjects) == 0 {
				value = 0
			} else {
				value = sum / float64(len(subjects))
			}
		default:
			return GroupSummary{}, fmt.Errorf("dataset: unknown statistic %q", statistic)
		}
	}
	for _, subject := range subjects {
		record := records[subject]
		if record.Sensitive {
			sensitiveCount++
			sensitive = true
		}
	}
	return GroupSummary{
		Key:            group,
		Subjects:       subjects,
		Count:          count,
		Value:          value,
		Sensitive:      sensitive,
		SensitiveCount: sensitiveCount,
	}, nil
}

// MaxAbsValue returns the maximum absolute attribute value in the dataset.
// It can be used as the global sensitivity of a sum query.
func (d *Dataset) MaxAbsValue(attribute string) float64 {
	max := 0.0
	for _, records := range d.groups {
		for _, record := range records {
			if value := math.Abs(record.Values[attribute]); value > max {
				max = value
			}
		}
	}
	if max == 0 {
		return 1
	}
	return max
}
