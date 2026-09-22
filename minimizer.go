package main

import (
	"fmt"
	"sort"
)

const (
	VerdictReproduced       = "reproduced_safety_failure"
	VerdictNoFailure        = "no_failure"
	VerdictLivenessFailure  = "liveness_timeout"
	VerdictDifferentFailure = "different_safety_failure"
	VerdictRejected         = "dependency_invalid"
)

type Trial struct {
	Stage         string   `json:"stage"`
	Granularity   int      `json:"granularity,omitempty"`
	Removed       []string `json:"removed"`
	Kept          []string `json:"kept"`
	Verdict       string   `json:"verdict"`
	Reason        string   `json:"reason,omitempty"`
	SafetyCount   int      `json:"safety_count,omitempty"`
	LivenessCount int      `json:"liveness_count,omitempty"`
}

type NecessityAudit struct {
	EventID string `json:"event_id"`
	Verdict string `json:"verdict"`
	Reason  string `json:"reason,omitempty"`
}

type ReductionResult struct {
	Original          []Event          `json:"original"`
	Minimal           []Event          `json:"minimal_counterexample"`
	TargetFingerprint string           `json:"target_fingerprint"`
	Removed           []string         `json:"removed_event_ids"`
	Retained          []string         `json:"retained_event_ids"`
	Trials            []Trial          `json:"reduction_process"`
	NecessityAudit    []NecessityAudit `json:"singleton_necessity_audit"`
	FinalReport       *Report          `json:"final_report"`
}

func classifyCandidate(events []Event, targetFingerprint string) (*Report, string, string) {
	report, err := Replay(events)
	if err != nil {
		return nil, VerdictRejected, err.Error()
	}
	fingerprint := safetyFingerprint(report.Safety)
	switch {
	case fingerprint == targetFingerprint && len(report.Liveness) == 0:
		return report, VerdictReproduced, ""
	case len(report.Safety) > 0:
		return report, VerdictDifferentFailure, "safety fingerprint changed: " + fingerprint
	case len(report.Liveness) > 0:
		return report, VerdictLivenessFailure, "only deadline/liveness violations remain"
	default:
		return report, VerdictNoFailure, "no safety violation remains"
	}
}

func MinimizeCounterexample(events []Event) (*ReductionResult, error) {
	initial, err := Replay(events)
	if err != nil {
		return nil, fmt.Errorf("initial trace is not replayable: %w", err)
	}
	if len(initial.Safety) == 0 {
		return nil, fmt.Errorf("initial trace has no consistency/safety violation")
	}
	if len(initial.Liveness) != 0 {
		return nil, fmt.Errorf("initial trace mixes liveness and safety failures")
	}
	target := safetyFingerprint(initial.Safety)
	kept := make([]int, len(events))
	for i := range events {
		kept[i] = i
	}
	result := &ReductionResult{
		Original: append([]Event(nil), events...), TargetFingerprint: target,
	}

	for granularity := 2; granularity <= len(kept) && len(kept) > 1; {
		chunks := balancedChunks(kept, granularity)
		removedAny := false
		for _, chunk := range chunks {
			chunkSet := map[int]bool{}
			for _, index := range chunk {
				chunkSet[index] = true
			}
			candidateIndexes := make([]int, 0, len(kept)-len(chunk))
			removedIDs := make([]string, 0, len(chunk))
			for _, index := range kept {
				if chunkSet[index] {
					removedIDs = append(removedIDs, events[index].ID)
				} else {
					candidateIndexes = append(candidateIndexes, index)
				}
			}
			candidate := selectEvents(events, candidateIndexes)
			report, verdict, reason := classifyCandidate(candidate, target)
			trial := Trial{
				Stage: "ddmin", Granularity: granularity,
				Removed: removedIDs, Kept: eventIDs(candidate),
				Verdict: verdict, Reason: reason,
			}
			if report != nil {
				trial.SafetyCount = len(report.Safety)
				trial.LivenessCount = len(report.Liveness)
			}
			result.Trials = append(result.Trials, trial)
			if verdict == VerdictReproduced {
				kept = candidateIndexes
				removedAny = true
				granularity = 2
				break
			}
		}
		if !removedAny {
			if granularity < len(kept) {
				granularity = min(granularity*2, len(kept))
			} else {
				break
			}
		}
	}

	for _, index := range kept {
		candidateIndexes := make([]int, 0, len(kept)-1)
		for _, other := range kept {
			if other != index {
				candidateIndexes = append(candidateIndexes, other)
			}
		}
		candidate := selectEvents(events, candidateIndexes)
		_, verdict, reason := classifyCandidate(candidate, target)
		result.NecessityAudit = append(result.NecessityAudit, NecessityAudit{
			EventID: events[index].ID, Verdict: verdict, Reason: reason,
		})
	}

	result.Minimal = selectEvents(events, kept)
	final, _, _ := classifyCandidate(result.Minimal, target)
	result.FinalReport = final
	result.Retained = eventIDs(result.Minimal)
	keptSet := map[int]bool{}
	for _, index := range kept {
		keptSet[index] = true
	}
	for i, ev := range events {
		if !keptSet[i] {
			result.Removed = append(result.Removed, ev.ID)
		}
	}
	sort.Strings(result.Removed)
	return result, nil
}

func balancedChunks(values []int, count int) [][]int {
	if count > len(values) {
		count = len(values)
	}
	if count <= 0 {
		return nil
	}
	chunks := make([][]int, 0, count)
	base := len(values) / count
	extra := len(values) % count
	start := 0
	for i := 0; i < count; i++ {
		size := base
		if i < extra {
			size++
		}
		chunk := append([]int(nil), values[start:start+size]...)
		chunks = append(chunks, chunk)
		start += size
	}
	return chunks
}

func selectEvents(events []Event, indexes []int) []Event {
	selected := make([]Event, 0, len(indexes))
	for _, index := range indexes {
		selected = append(selected, events[index])
	}
	return selected
}

func eventIDs(events []Event) []string {
	ids := make([]string, 0, len(events))
	for _, ev := range events {
		ids = append(ids, ev.ID)
	}
	return ids
}
