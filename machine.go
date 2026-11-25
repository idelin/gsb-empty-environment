package upgradeplan

import (
	"fmt"
)

// Outcome is one concrete device-choice outcome for one broadcast.
type Outcome struct {
	// SuccessCount is the server-visible count for this broadcast.
	SuccessCount int `json:"success_count"`

	// Eligible contains canonical fleet indices that matched the release.
	Eligible []int `json:"eligible,omitempty"`

	// Assignment is the chosen new-set option index for each Eligible entry.
	Assignment []int `json:"assignment,omitempty"`

	// ChosenSets contains the selected successor sets in eligible-index order.
	ChosenSets []SignerSet `json:"chosen_sets,omitempty"`

	// Fleet is the canonical successor fleet.
	Fleet Fleet `json:"fleet"`
}

// EligibleIndices returns devices in canonical fleet order that match package,
// SDK interval, and old-signer threshold.
func EligibleIndices(devices []DeviceState, release Release) []int {
	fleet := CanonicalFleet(devices)
	return eligibleIndicesCanonical(fleet, release)
}

func eligibleIndicesCanonical(fleet Fleet, release Release) []int {
	var out []int
	for i, device := range fleet {
		if !packageMatches(device.Package, release.Package) {
			continue
		}
		if !sdkMatches(device, release.SDKRange) {
			continue
		}
		if OldKeyVoteCount(device.Signers, release) < release.OldThreshold {
			continue
		}
		out = append(out, i)
	}
	return out
}

func packageMatches(devicePackage, releasePackage string) bool {
	if devicePackage == "" || releasePackage == "" {
		return true
	}
	return devicePackage == releasePackage
}

func sdkMatches(device DeviceState, interval SDKRange) bool {
	if device.SDK > 0 {
		return interval.Contains(device.SDK)
	}
	if device.SDKRange.MinSDK != 0 || device.SDKRange.MaxSDK != 0 {
		// An interval-valued finite state matches when the two intervals overlap.
		return interval.Overlaps(device.SDKRange)
	}
	return interval.Contains(0)
}

// OldKeyVoteCount counts distinct public-key materials in current signers that
// satisfy the record's old-signer requirement. Two IDs for one key cast one vote.
func OldKeyVoteCount(current SignerSet, release Release) int {
	current = current.Normalize()
	oldSigners := append(SignerSet{}, release.OldSigners...)
	for _, key := range release.OldSignerKeys {
		oldSigners = append(oldSigners, Signer{ID: key, KeyMaterial: key})
	}
	oldSigners = oldSigners.Normalize()

	if len(oldSigners) == 0 {
		return current.DistinctKeyCount()
	}

	acceptedMaterial := make(map[string]struct{})
	acceptedID := make(map[string]struct{})
	for _, signer := range oldSigners {
		if signer.publicMaterial() != "" {
			acceptedMaterial[signer.Material()] = struct{}{}
		}
		if signer.ID != "" {
			acceptedID[signer.ID] = struct{}{}
		}
	}

	votedMaterial := make(map[string]struct{})
	for _, signer := range current {
		_, keyOK := acceptedMaterial[signer.Material()]
		_, idOK := acceptedID[signer.ID]
		if keyOK || idOK {
			votedMaterial[signer.Material()] = struct{}{}
		}
	}
	return len(votedMaterial)
}

// EnumerateOutcomes enumerates every eligible-device choice. The success count
// is the number of eligible devices; only successor signer sets are chosen.
func EnumerateOutcomes(devices []DeviceState, release Release) ([]Outcome, error) {
	fleet := CanonicalFleet(devices)
	return enumerateCanonicalOutcomes(fleet, release)
}

func enumerateCanonicalOutcomes(fleet Fleet, release Release) ([]Outcome, error) {
	options := release.NewSignerSets
	if len(options) == 0 && len(release.NewSigners) > 0 {
		options = []SignerSet{release.NewSigners}
	}
	if len(options) == 0 {
		return nil, fmt.Errorf("upgradeplan: release %q has no new signer set", release.ID)
	}
	normalizedOptions := make([]SignerSet, len(options))
	for i, option := range options {
		normalizedOptions[i] = option.Normalize()
	}

	eligible := eligibleIndicesCanonical(fleet, release)
	optionCounts := make([]int, len(eligible))
	for i := range eligible {
		optionCounts[i] = len(normalizedOptions)
	}
	assignments := cartesianAssignments(optionCounts)

	outcomes := make([]Outcome, 0, len(assignments))
	for _, assignment := range assignments {
		next := append(Fleet(nil), fleet...)
		chosen := make([]SignerSet, len(eligible))
		for j, deviceIndex := range eligible {
			set := normalizedOptions[assignment[j]]
			chosen[j] = set
			next[deviceIndex] = upgradeDevice(fleet[deviceIndex], release, set)
		}
		outcomes = append(outcomes, Outcome{
			SuccessCount: len(eligible),
			Eligible:     append([]int(nil), eligible...),
			Assignment:   append([]int(nil), assignment...),
			ChosenSets:   chosen,
			Fleet:        CanonicalFleet(next),
		})
	}
	return outcomes, nil
}

func cartesianAssignments(counts []int) [][]int {
	if len(counts) == 0 {
		return [][]int{{}}
	}
	total := 1
	for _, count := range counts {
		if count <= 0 {
			return nil
		}
		total *= count
	}
	out := make([][]int, 0, total)
	current := make([]int, len(counts))
	var walk func(position int)
	walk = func(position int) {
		if position == len(counts) {
			out = append(out, append([]int(nil), current...))
			return
		}
		for choice := 0; choice < counts[position]; choice++ {
			current[position] = choice
			walk(position + 1)
		}
	}
	walk(0)
	return out
}

func upgradeDevice(device DeviceState, release Release, nextSigners SignerSet) DeviceState {
	next := device
	next.Signers = nextSigners.Normalize()

	switch {
	case device.DataLost:
		// Sticky: a later signed upgrade cannot retroactively restore lineage.
		next.DataLost = true
	case device.InitialData && !release.CarryExistingData:
		next.DataLost = true
		next.HasData = false
	case device.InitialData && release.CarryExistingData:
		next.HasData = true
	}
	return next
}

func transitionBelief(belief *Belief, release Release) map[int]*Belief {
	worldsByCount := make(map[int][][]DeviceState)
	for _, fleet := range belief.Worlds {
		outcomes, err := EnumerateOutcomes(fleet, release)
		if err != nil {
			// Normalized problems validate releases before search.
			continue
		}
		for _, outcome := range outcomes {
			worldsByCount[outcome.SuccessCount] = append(worldsByCount[outcome.SuccessCount], []DeviceState(outcome.Fleet))
		}
	}

	out := make(map[int]*Belief, len(worldsByCount))
	for count, worlds := range worldsByCount {
		out[count] = NewBelief(worlds...)
	}
	return out
}
