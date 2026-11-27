package topology

import (
	"fmt"
	"math"
	"sort"
)

// AllocateHints runs the control-plane topology hint allocator.
//
// It merges and de-duplicates the EndpointSlices, validates every topology
// safeguard and, when safe, assigns each ready endpoint a forZones hint. The
// returned Allocation is never nil: a safeguard produces Active == false with
// a precise ReasonCode and Message, and an empty Hints map (global routing).
func AllocateHints(cfg ServiceConfig, zones []ZoneCapacity, slices []EndpointSlice) *Allocation {
	threshold, err := resolveThreshold(cfg.OverloadThreshold)
	if err != nil {
		return failed(ReasonInvalidConfig, err.Error(), threshold, nil, nil)
	}

	if !hintsEnabled(cfg) {
		return failed(
			ReasonHintsDisabled,
			"topology hints disabled by service configuration (trafficDistribution or topologyHintsEnabled)",
			threshold, nil, nil,
		)
	}

	if len(slices) == 0 {
		return failed(ReasonNoInput, "no EndpointSlice input provided", threshold, nil, nil)
	}

	merged := MergeSlices(slices)
	if len(merged) == 0 {
		return failed(ReasonNoInput, "EndpointSlices contain no endpoint with an IP", threshold, nil, nil)
	}

	activeZones, fail := validateZones(zones)
	if fail != nil {
		return failed(fail.reason, fail.message, threshold, nil, nil)
	}

	ready := make([]Endpoint, 0, len(merged))
	for _, ep := range merged {
		if ep.Conditions.Ready {
			ready = append(ready, ep)
		}
	}

	if len(ready) < len(activeZones) {
		return failed(
			ReasonEndpointZoneCount,
			fmt.Sprintf("ready endpoints %d are fewer than active zones %d", len(ready), len(activeZones)),
			threshold, ready, nil,
		)
	}

	totalCPU := 0.0
	for _, zone := range activeZones {
		totalCPU += zone.CPU
	}

	shares := make(map[string]float64, len(activeZones))
	minimum := make(map[string]int, len(activeZones))
	for _, zone := range activeZones {
		share := zone.CPU / totalCPU
		shares[zone.Zone] = share
		minimum[zone.Zone] = minimumEndpoints(share, len(ready), threshold)
	}

	shortZones := make([]string, 0)
	minimumTotal := 0
	for _, zone := range activeZones {
		minimumTotal += minimum[zone.Zone]
		if minimum[zone.Zone] == 0 {
			shortZones = append(shortZones, zone.Zone)
		}
	}
	if minimumTotal > len(ready) {
		return failed(
			ReasonOverload,
			fmt.Sprintf(
				"minimum endpoint quota %d exceeds %d ready endpoints at overload threshold %.3f; per-zone minimum: %s",
				minimumTotal, len(ready), threshold, describeMinimum(activeZones, minimum),
			),
			threshold, ready, minimum,
		)
	}
	if len(shortZones) > 0 {
		return failed(
			ReasonOverload,
			fmt.Sprintf("zones %v cannot receive a non-zero minimum quota", shortZones),
			threshold, ready, minimum,
		)
	}

	hints := assignEndpoints(activeZones, ready, shares, minimum)
	if hints == nil {
		return failed(
			ReasonOverload,
			"allocation could not satisfy every zone minimum quota without overload",
			threshold, ready, minimum,
		)
	}

	return &Allocation{
		Active:           true,
		Reason:           ReasonOK,
		Message:          "topology hints allocated",
		Threshold:        threshold,
		Hints:            hints,
		ReadyEndpoints:   ready,
		ZoneShares:       shares,
		MinimumEndpoints: minimum,
	}
}

type zoneFailure struct {
	reason  ReasonCode
	message string
}

func resolveThreshold(configured float64) (float64, error) {
	switch {
	case configured == 0:
		return DefaultOverloadThreshold, nil
	case configured < 1:
		return DefaultOverloadThreshold, fmt.Errorf("overloadThreshold %.3f must be >= 1.0 (0 means default %.1f)", configured, DefaultOverloadThreshold)
	default:
		return configured, nil
	}
}

func hintsEnabled(cfg ServiceConfig) bool {
	if cfg.TrafficDistribution == TrafficDistributionCluster {
		return false
	}
	return cfg.TrafficDistribution == TrafficDistributionPreferClose || cfg.TopologyHintsEnabled
}

func validateZones(zones []ZoneCapacity) ([]ZoneCapacity, *zoneFailure) {
	active := make([]ZoneCapacity, 0, len(zones))
	for _, zone := range zones {
		if zone.CPU < 0 {
			return nil, &zoneFailure{
				reason:  ReasonInvalidConfig,
				message: fmt.Sprintf("zone %q reports negative CPU capacity %v", zone.Zone, zone.CPU),
			}
		}
		active = append(active, zone)
	}
	sort.Slice(active, func(i, j int) bool { return active[i].Zone < active[j].Zone })

	if len(active) < 2 {
		return nil, &zoneFailure{
			reason:  ReasonInsufficientZones,
			message: fmt.Sprintf("topology routing needs at least 2 active zones, found %d", len(active)),
		}
	}

	for _, zone := range active {
		if zone.CPU == 0 {
			return nil, &zoneFailure{
				reason:  ReasonZeroCPUZone,
				message: fmt.Sprintf("zone %q has zero CPU capacity and cannot serve traffic", zone.Zone),
			}
		}
	}
	return active, nil
}

// minimumEndpoints is ceil(share * endpointCount / threshold): the smallest
// endpoint count whose per-endpoint load stays within the overload threshold.
func minimumEndpoints(share float64, endpointCount int, threshold float64) int {
	required := share * float64(endpointCount) / threshold
	return int(math.Ceil(required - 1e-9))
}

func describeMinimum(zones []ZoneCapacity, minimum map[string]int) string {
	description := ""
	for i, zone := range zones {
		if i > 0 {
			description += ", "
		}
		description += fmt.Sprintf("%s=%d", zone.Zone, minimum[zone.Zone])
	}
	return description
}

func failed(
	reason ReasonCode,
	message string,
	threshold float64,
	ready []Endpoint,
	minimum map[string]int,
) *Allocation {
	return &Allocation{
		Active:           false,
		Reason:           reason,
		Message:          message,
		Threshold:        threshold,
		Hints:            nil,
		ReadyEndpoints:   ready,
		MinimumEndpoints: minimum,
		ZoneShares:       nil,
	}
}

// assignEndpoints performs the deterministic three-phase allocation:
//  1. every zone keeps as many local endpoints as its minimum quota allows;
//  2. deficit zones steal cross-zone endpoints from the zones with the largest
//     surplus first, minimizing the overload ratio created by each steal;
//  3. any leftover endpoints go to the zone whose resulting overload ratio is
//     smallest (home zone preferred on ties for locality).
func assignEndpoints(
	zones []ZoneCapacity,
	ready []Endpoint,
	shares map[string]float64,
	minimum map[string]int,
) map[string]HintsForZones {
	zoneNames := make([]string, 0, len(zones))
	for _, zone := range zones {
		zoneNames = append(zoneNames, zone.Zone)
	}

	count := make(map[string]int, len(zoneNames))
	home := make(map[string][]Endpoint)
	unallocated := make([]Endpoint, 0, len(ready))

	for _, ep := range ready {
		if _, known := shares[ep.Zone]; known {
			home[ep.Zone] = append(home[ep.Zone], ep)
		} else {
			unallocated = append(unallocated, ep)
		}
	}
	for zone := range home {
		sort.Slice(home[zone], func(i, j int) bool { return home[zone][i].IP < home[zone][j].IP })
	}
	sort.Slice(unallocated, func(i, j int) bool { return unallocated[i].IP < unallocated[j].IP })

	assignment := make(map[string]string, len(ready))

	// Phase 1: local-first minimum quota allocation.
	for _, zone := range zoneNames {
		need := minimum[zone]
		locals := home[zone]
		take := need
		if take > len(locals) {
			take = len(locals)
		}
		for i := 0; i < take; i++ {
			assignment[locals[i].IP] = zone
			count[zone]++
		}
		home[zone] = locals[take:]
	}

	// Phase 2: fill every remaining deficit with minimum cross-zone overload.
	for {
		target := pickDeficitZone(zoneNames, count, minimum)
		if target == "" {
			break
		}
		donor := pickDonorEndpoint(zoneNames, home, &unallocated, count, minimum)
		if donor == nil {
			return nil
		}
		assignment[donor.IP] = target
		count[target]++
	}

	// Phase 3: distribute leftover endpoints by smallest resulting overload.
	remaining := collectUnallocated(zoneNames, home, unallocated)
	for _, ep := range remaining {
		target := pickLowestLoadZone(zoneNames, ep, count, shares, len(ready))
		assignment[ep.IP] = target
		count[target]++
	}

	hints := make(map[string]HintsForZones, len(assignment))
	for ip, zone := range assignment {
		hints[ip] = HintsForZones{ForZones: []string{zone}}
	}
	return hints
}

func pickDeficitZone(zones []string, count, minimum map[string]int) string {
	choice := ""
	for _, zone := range zones {
		if count[zone] >= minimum[zone] {
			continue
		}
		if choice == "" || minimum[zone]-count[zone] > minimum[choice]-count[choice] {
			choice = zone
		}
	}
	return choice
}

// pickDonorEndpoint returns the unallocated endpoint whose home zone has the
// largest surplus, so each cross-zone steal creates the smallest overload
// increase. Endpoints without a known home zone are always preferred because
// taking them cannot overload any zone.
func pickDonorEndpoint(
	zones []string,
	home map[string][]Endpoint,
	homeless *[]Endpoint,
	count, minimum map[string]int,
) *Endpoint {
	if len(*homeless) > 0 {
		chosen := (*homeless)[0]
		*homeless = (*homeless)[1:]
		return &chosen
	}

	bestZone := ""
	for _, zone := range zones {
		if len(home[zone]) == 0 {
			continue
		}
		surplus := count[zone] - minimum[zone]
		bestSurplus := 0
		if bestZone != "" {
			bestSurplus = count[bestZone] - minimum[bestZone]
		}
		if bestZone == "" || surplus > bestSurplus {
			bestZone = zone
		}
	}
	if bestZone == "" {
		return nil
	}

	chosen := home[bestZone][0]
	home[bestZone] = home[bestZone][1:]
	return &chosen
}

func collectUnallocated(
	zones []string,
	home map[string][]Endpoint,
	homeless []Endpoint,
) []Endpoint {
	remaining := append([]Endpoint{}, homeless...)
	for _, zone := range zones {
		remaining = append(remaining, home[zone]...)
	}
	sort.Slice(remaining, func(i, j int) bool { return remaining[i].IP < remaining[j].IP })
	return remaining
}

// pickLowestLoadZone minimizes expectedShare/actualShare after the endpoint is
// added. Ties prefer the endpoint home zone, then the lexicographically first
// zone.
func pickLowestLoadZone(
	zones []string,
	ep Endpoint,
	count map[string]int,
	shares map[string]float64,
	endpointCount int,
) string {
	choice := ""
	choiceScore := 0.0
	for _, zone := range zones {
		expected := shares[zone] * float64(endpointCount)
		actual := float64(count[zone] + 1)
		score := expected / actual
		switch {
		case choice == "":
			choice, choiceScore = zone, score
		case score < choiceScore-1e-12:
			choice, choiceScore = zone, score
		case math.Abs(score-choiceScore) <= 1e-12:
			if ep.Zone == zone && ep.Zone != choice {
				choice, choiceScore = zone, score
			} else if zone < choice {
				choice, choiceScore = zone, score
			}
		}
	}
	return choice
}
