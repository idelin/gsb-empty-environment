package topology

import "sort"

// MergeSlices flattens EndpointSlices and de-duplicates endpoints by IP.
//
// Merge rules for repeated IPs (deterministic regardless of input order):
//   - identity fields (Zone, Node) come from the lexicographically first
//     (slice name, input index) occurrence so the merged view is stable;
//   - condition flags are AND-ed: the merged endpoint is ready/serving/
//     terminating only when every occurrence agrees;
//   - Weight is the maximum non-zero weight (zero means unset).
//
// Endpoints without an IP are dropped. The returned slice is sorted by IP.
func MergeSlices(slices []EndpointSlice) []Endpoint {
	type record struct {
		endpoint    Endpoint
		sourceOrder int
		seen        int
	}

	byIP := make(map[string]*record)
	order := 0

	sliceNames := make([]string, 0, len(slices))
	for _, s := range slices {
		sliceNames = append(sliceNames, s.Name)
	}
	sort.Strings(sliceNames)

	sliceIndex := make(map[string]int, len(sliceNames))
	for i, name := range sliceNames {
		sliceIndex[name] = i
	}

	for _, s := range slices {
		for _, ep := range s.Endpoints {
			if ep.IP == "" {
				continue
			}
			candidate := ep
			candidate.SourceSlice = s.Name
			order++

			existing, ok := byIP[ep.IP]
			if !ok {
				byIP[ep.IP] = &record{endpoint: candidate, sourceOrder: order, seen: 1}
				continue
			}

			existing.seen++
			existing.endpoint.Conditions.Ready = existing.endpoint.Conditions.Ready &&
				candidate.Conditions.Ready
			existing.endpoint.Conditions.Serving = existing.endpoint.Conditions.Serving &&
				candidate.Conditions.Serving
			existing.endpoint.Conditions.Terminating = existing.endpoint.Conditions.Terminating &&
				candidate.Conditions.Terminating

			if candidate.Weight > existing.endpoint.Weight {
				existing.endpoint.Weight = candidate.Weight
			}

			// Keep identity fields from the lexicographically first slice.
			if sliceIndex[s.Name] < sliceIndex[existing.endpoint.SourceSlice] {
				existing.endpoint.Zone = candidate.Zone
				existing.endpoint.Node = candidate.Node
				existing.endpoint.SourceSlice = candidate.SourceSlice
			}
		}
	}

	ips := make([]string, 0, len(byIP))
	for ip := range byIP {
		ips = append(ips, ip)
	}
	sort.Strings(ips)

	merged := make([]Endpoint, 0, len(ips))
	for _, ip := range ips {
		merged = append(merged, byIP[ip].endpoint)
	}
	return merged
}

// AggregateZoneCapacity sums node CPU capacities into per-zone totals.
// Nodes with negative CPU are ignored; zones with no positive node are absent
// from the result.
func AggregateZoneCapacity(nodes []NodeCapacity) []ZoneCapacity {
	totals := make(map[string]float64)
	for _, node := range nodes {
		if node.CPUCapacity < 0 {
			continue
		}
		totals[node.Zone] += node.CPUCapacity
	}

	zones := make([]ZoneCapacity, 0, len(totals))
	for zone, cpu := range totals {
		zones = append(zones, ZoneCapacity{Zone: zone, CPU: cpu})
	}
	sort.Slice(zones, func(i, j int) bool { return zones[i].Zone < zones[j].Zone })
	return zones
}
