package taskpkg

import (
	"fmt"
	"sort"
)

const maxUpdateRegions = 12

type compiledRegion struct {
	id           string
	current      string
	target       string
	currentEdges []Edge
	targetEdges  []Edge
	canConfirm   bool
	required     bool
	bit          uint64
}

type worldInfo struct {
	mutableEdges int

	attackBlocked bool
	attackFrom    string
	attackTo      string

	managementPreserved bool
	managementFrom      string
	managementTo        string
}

func (w worldInfo) safe() bool {
	return w.attackBlocked && w.managementPreserved
}

type model struct {
	regions  []compiledRegion
	required []int
	byID     map[string]int
	lost     map[string]bool

	fixedEdges    []Edge
	attackEntries []string
	secrets       map[string]struct{}
	management    []ManagementPath
	worlds        []worldInfo
}

func compile(req Request) (*model, error) {
	m := &model{
		byID:          make(map[string]int, len(req.Regions)),
		lost:          make(map[string]bool, len(req.LostRegions)),
		fixedEdges:    dedupeEdges(req.FixedEdges),
		attackEntries: dedupeStrings(req.AttackEntries),
		secrets:       stringSet(req.Secrets),
		management:    dedupeManagementPaths(req.ManagementPaths),
	}

	for _, id := range req.LostRegions {
		m.lost[id] = true
	}

	for i, region := range req.Regions {
		if region.ID == "" {
			return nil, invalid("region %d has an empty id", i)
		}
		if _, exists := m.byID[region.ID]; exists {
			return nil, invalid("duplicate region id %q", region.ID)
		}
		if region.CurrentVersion == "" || region.TargetVersion == "" {
			return nil, invalid("region %q must name both current and target versions", region.ID)
		}

		versions := make(map[string][]Edge, len(region.Versions))
		for vi, version := range region.Versions {
			if version.Name == "" {
				return nil, invalid("region %q has an empty version name at index %d", region.ID, vi)
			}
			if _, exists := versions[version.Name]; exists {
				return nil, invalid("region %q has duplicate version %q", region.ID, version.Name)
			}
			versions[version.Name] = dedupeEdges(version.Edges)
		}

		currentEdges, ok := versions[region.CurrentVersion]
		if !ok {
			return nil, invalid("region %q has no installable current version %q", region.ID, region.CurrentVersion)
		}
		targetEdges, ok := versions[region.TargetVersion]
		if !ok {
			return nil, invalid("region %q has no installable target version %q", region.ID, region.TargetVersion)
		}

		m.byID[region.ID] = len(m.regions)
		m.regions = append(m.regions, compiledRegion{
			id:           region.ID,
			current:      region.CurrentVersion,
			target:       region.TargetVersion,
			currentEdges: currentEdges,
			targetEdges:  targetEdges,
			canConfirm:   region.CanConfirm,
			required:     region.CurrentVersion != region.TargetVersion,
		})
	}

	for id := range m.lost {
		if _, ok := m.byID[id]; !ok {
			return nil, invalid("lost region %q is not present in the topology", id)
		}
	}

	for i := range m.regions {
		if m.regions[i].required {
			m.required = append(m.required, i)
		}
	}
	if len(m.required) > maxUpdateRegions {
		return nil, fmt.Errorf("%w: %d regions require updates, limit is %d",
			ErrMutableEdgeLimit, len(m.required), maxUpdateRegions)
	}

	sort.SliceStable(m.required, func(a, b int) bool {
		return m.regions[m.required[a]].id < m.regions[m.required[b]].id
	})
	for bit, regionIndex := range m.required {
		m.regions[regionIndex].bit = uint64(1) << uint(bit)
	}

	if err := m.buildWorlds(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *model) buildWorlds() error {
	size := 1 << len(m.required)
	m.worlds = make([]worldInfo, size)

	for mask := 0; mask < size; mask++ {
		edges, mutableEdges := m.edgesForMask(mask)
		if len(mutableEdges) > MaxMutableEdges {
			return fmt.Errorf("%w: configuration has %d mutable filter edges, limit is %d",
				ErrMutableEdgeLimit, len(mutableEdges), MaxMutableEdges)
		}
		m.worlds[mask] = inspectWorld(edges, len(mutableEdges),
			m.attackEntries, m.secrets, m.management)
	}
	return nil
}

func (m *model) edgesForMask(mask int) ([]Edge, []Edge) {
	all := make([]Edge, 0, len(m.fixedEdges)+len(m.required))
	mutable := make([]Edge, 0, len(m.required))
	all = append(all, m.fixedEdges...)

	for i := range m.regions {
		region := &m.regions[i]
		active := region.currentEdges
		if region.required && mask&int(region.bit) != 0 {
			active = region.targetEdges
		}
		mutable = append(mutable, active...)
		all = append(all, active...)
	}

	return dedupeEdges(all), dedupeEdges(mutable)
}

func inspectWorld(edges []Edge, mutableEdges int, attackEntries []string,
	secrets map[string]struct{}, management []ManagementPath) worldInfo {

	info := worldInfo{
		mutableEdges:        mutableEdges,
		attackBlocked:       true,
		managementPreserved: true,
	}
	adj := adjacency(edges)

	for _, source := range attackEntries {
		target, found := reachable(adj, source, secrets)
		if found {
			info.attackBlocked = false
			info.attackFrom = source
			info.attackTo = target
			break
		}
	}

	for _, path := range management {
		if !isReachable(adj, path.From, path.To) {
			info.managementPreserved = false
			info.managementFrom = path.From
			info.managementTo = path.To
			break
		}
	}

	return info
}

func adjacency(edges []Edge) map[string][]string {
	adj := make(map[string][]string)
	for _, edge := range edges {
		adj[edge.From] = append(adj[edge.From], edge.To)
		if _, exists := adj[edge.To]; !exists {
			adj[edge.To] = nil
		}
	}
	return adj
}

func reachable(adj map[string][]string, start string, targets map[string]struct{}) (string, bool) {
	if _, ok := targets[start]; ok {
		return start, true
	}
	if len(targets) == 0 {
		return "", false
	}

	seen := map[string]bool{start: true}
	queue := []string{start}
	for head := 0; head < len(queue); head++ {
		node := queue[head]
		for _, next := range adj[node] {
			if seen[next] {
				continue
			}
			seen[next] = true
			if _, ok := targets[next]; ok {
				return next, true
			}
			queue = append(queue, next)
		}
	}
	return "", false
}

func isReachable(adj map[string][]string, from, to string) bool {
	if from == to {
		return true
	}
	target := map[string]struct{}{to: {}}
	_, ok := reachable(adj, from, target)
	return ok
}

func dedupeEdges(edges []Edge) []Edge {
	if len(edges) == 0 {
		return nil
	}
	type key struct {
		from string
		to   string
	}
	seen := make(map[key]bool, len(edges))
	out := make([]Edge, 0, len(edges))
	for _, edge := range edges {
		k := key{from: edge.From, to: edge.To}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, edge)
	}
	return out
}

func dedupeManagementPaths(paths []ManagementPath) []ManagementPath {
	if len(paths) == 0 {
		return nil
	}
	type key struct {
		from string
		to   string
	}
	seen := make(map[key]bool, len(paths))
	out := make([]ManagementPath, 0, len(paths))
	for _, path := range paths {
		k := key{from: path.From, to: path.To}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, path)
	}
	return out
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}
