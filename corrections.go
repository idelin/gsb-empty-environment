package main

import (
	"fmt"
	"time"
)

// Correction is a late-arriving fix to a previously recorded event. Applying
// it rewrites history, so the matching and all stats are recomputed.
type Correction struct {
	Kind    string     `json:"kind"` // "fault" or "alert"
	ID      string     `json:"id"`
	Entity  *string    `json:"entity,omitempty"`
	Symptom *string    `json:"symptom,omitempty"`
	Time    *time.Time `json:"time,omitempty"`  // new alert timestamp
	Start   *time.Time `json:"start,omitempty"` // new fault start
	End     *time.Time `json:"end,omitempty"`   // new fault end
}

func applyCorrection(c Correction, faults []Fault, alerts []Alert) error {
	switch c.Kind {
	case "alert":
		for i := range alerts {
			if alerts[i].ID == c.ID {
				if c.Entity != nil {
					alerts[i].Entity = *c.Entity
				}
				if c.Symptom != nil {
					alerts[i].Symptom = *c.Symptom
				}
				if c.Time != nil {
					alerts[i].Time = *c.Time
				}
				return nil
			}
		}
		return fmt.Errorf("correction: alert %q not found", c.ID)
	case "fault":
		for i := range faults {
			if faults[i].ID == c.ID {
				if c.Entity != nil {
					faults[i].Entity = *c.Entity
				}
				if c.Symptom != nil {
					faults[i].Symptom = *c.Symptom
				}
				if c.Start != nil {
					faults[i].Start = *c.Start
				}
				if c.End != nil {
					end := *c.End
					faults[i].End = &end
				}
				return nil
			}
		}
		return fmt.Errorf("correction: fault %q not found", c.ID)
	default:
		return fmt.Errorf("correction: unknown kind %q", c.Kind)
	}
}

// HistoryEntry records how one correction changed the matching and stats.
type HistoryEntry struct {
	Correction Correction
	Added      []Pair
	Removed    []Pair
	Stats      Stats
}

func pairKey(p Pair) string { return p.FaultID + "->" + p.AlertID }

func diffPairs(oldPairs, newPairs []Pair) (added, removed []Pair) {
	oldSet := make(map[string]bool)
	for _, p := range oldPairs {
		oldSet[pairKey(p)] = true
	}
	newSet := make(map[string]bool)
	for _, p := range newPairs {
		newSet[pairKey(p)] = true
		if !oldSet[pairKey(p)] {
			added = append(added, p)
		}
	}
	for _, p := range oldPairs {
		if !newSet[pairKey(p)] {
			removed = append(removed, p)
		}
	}
	return added, removed
}

// ApplyCorrections replays corrections in order, re-matching after each one
// and recording the diff of historical matches plus the updated stats.
func ApplyCorrections(fx *Fixture) ([]HistoryEntry, error) {
	res := Match(fx.Faults, fx.Alerts, fx.Config)
	var hist []HistoryEntry
	for _, c := range fx.Corrections {
		if err := applyCorrection(c, fx.Faults, fx.Alerts); err != nil {
			return hist, err
		}
		next := Match(fx.Faults, fx.Alerts, fx.Config)
		added, removed := diffPairs(res.Pairs, next.Pairs)
		hist = append(hist, HistoryEntry{
			Correction: c,
			Added:      added,
			Removed:    removed,
			Stats:      computeStats(next),
		})
		res = next
	}
	return hist, nil
}
