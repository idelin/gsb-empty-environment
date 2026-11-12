package netsec

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strconv"
)

// MaxPlugins is the largest number of plugins an Analysis may contain.
const MaxPlugins = 20

// epsilon tolerates floating point rounding in rate comparisons.
const epsilon = 1e-12

// ErrUnreachable is returned when no plugin combination reaches the requested
// identification rate, even when every plugin is selected.
var ErrUnreachable = errors.New("netsec: requested identification rate is unreachable")

// Metric selects how the identification rate of a public partition is measured.
type Metric int

const (
	// MetricBayes is the one-shot identification probability: after observing
	// the public labels an adversary guesses the highest-prior secret inside
	// each equivalence class, so the rate is sum over classes of
	// max_{s in class} prior(s).
	MetricBayes Metric = iota

	// MetricSingleton is the total prior mass of secrets whose equivalence
	// class contains no other secret, i.e. the secrets fully revealed.
	MetricSingleton
)

// Plugin maps every secret of the finite domain to a public label. The empty
// string means the plugin abstains on that secret: it is handled as its own
// label value, so an abstaining secret only merges with another secret when
// every selected plugin emits the same (possibly empty) label for both. A
// plugin that abstains on every secret therefore reveals nothing at all.
type Plugin struct {
	Name   string
	Labels []string
}

// Class is one cell of the common refinement: a set of secrets that are
// indistinguishable under the selected plugin combination.
type Class struct {
	// Secrets are the secret names in domain order.
	Secrets []string
	// Members are the secret indices in domain order.
	Members []int
	// Mass is the aggregate prior probability of the class.
	Mass float64
	// Guess is the highest-prior member an adversary bets on (ties keep the
	// smallest domain index).
	Guess string
	// GuessMass is the prior probability of Guess.
	GuessMass float64
}

// Partition is the common refinement induced by one plugin combination.
type Partition struct {
	// Mask identifies the selected plugins by their Analysis indices.
	Mask uint32
	// Classes are ordered by the domain index of their first member.
	Classes []Class
	// BayesRate is the MetricBayes identification rate.
	BayesRate float64
	// SingletonRate is the MetricSingleton identification rate.
	SingletonRate float64
}

// Rate returns the identification rate under the requested metric.
func (p *Partition) Rate(metric Metric) float64 {
	if metric == MetricSingleton {
		return p.SingletonRate
	}
	return p.BayesRate
}

// Analysis holds a finite secret domain, the prior on it and at most
// MaxPlugins label-revealing plugins.
type Analysis struct {
	secrets []string
	plugins []Plugin
	weights []float64
	// ids[p][s] is the compact label id emitted by plugin p for secret s;
	// zero marks abstention and real labels start at one.
	ids [][]int32
}

// NewAnalysis validates the inputs and builds the compact label tables.
// When weights is nil every secret receives a uniform prior; otherwise it must
// have one non-negative entry per secret and a strictly positive total.
func NewAnalysis(secrets []string, plugins []Plugin, weights []float64) (*Analysis, error) {
	if len(secrets) == 0 {
		return nil, errors.New("netsec: secret domain must be non-empty")
	}
	if len(plugins) > MaxPlugins {
		return nil, fmt.Errorf("netsec: at most %d plugins allowed, got %d", MaxPlugins, len(plugins))
	}

	seenSecret := make(map[string]bool, len(secrets))
	for _, name := range secrets {
		if name == "" {
			return nil, errors.New("netsec: secret names must be non-empty")
		}
		if seenSecret[name] {
			return nil, fmt.Errorf("netsec: duplicate secret %q", name)
		}
		seenSecret[name] = true
	}

	seenPlugin := make(map[string]bool, len(plugins))
	for idx, plugin := range plugins {
		if plugin.Name == "" {
			return nil, fmt.Errorf("netsec: plugin at index %d has no name", idx)
		}
		if seenPlugin[plugin.Name] {
			return nil, fmt.Errorf("netsec: duplicate plugin name %q", plugin.Name)
		}
		seenPlugin[plugin.Name] = true
		if len(plugin.Labels) != len(secrets) {
			return nil, fmt.Errorf("netsec: plugin %q emits %d labels for %d secrets",
				plugin.Name, len(plugin.Labels), len(secrets))
		}
	}

	prior := make([]float64, len(secrets))
	switch {
	case weights == nil:
		for i := range prior {
			prior[i] = 1.0 / float64(len(secrets))
		}
	case len(weights) != len(secrets):
		return nil, fmt.Errorf("netsec: got %d weights for %d secrets", len(weights), len(secrets))
	default:
		total := 0.0
		for idx, weight := range weights {
			if math.IsNaN(weight) || weight < 0 {
				return nil, fmt.Errorf("netsec: weight for secret %q must be non-negative", secrets[idx])
			}
			total += weight
		}
		if total <= 0 {
			return nil, errors.New("netsec: total prior mass must be strictly positive")
		}
		for idx, weight := range weights {
			prior[idx] = weight / total
		}
	}

	ids := make([][]int32, len(plugins))
	for p, plugin := range plugins {
		labelID := map[string]int32{}
		column := make([]int32, len(secrets))
		for s, label := range plugin.Labels {
			if label == "" {
				continue
			}
			id, ok := labelID[label]
			if !ok {
				id = int32(len(labelID) + 1)
				labelID[label] = id
			}
			column[s] = id
		}
		ids[p] = column
	}

	secretCopy := append([]string(nil), secrets...)
	pluginCopy := append([]Plugin(nil), plugins...)
	return &Analysis{
		secrets: secretCopy,
		plugins: pluginCopy,
		weights: prior,
		ids:     ids,
	}, nil
}

// SecretCount reports the size of the finite secret domain.
func (a *Analysis) SecretCount() int { return len(a.secrets) }

// PluginCount reports the number of registered plugins.
func (a *Analysis) PluginCount() int { return len(a.plugins) }

// SecretName returns the domain secret at the given index.
func (a *Analysis) SecretName(index int) string { return a.secrets[index] }

// PluginName returns the plugin name at the given index.
func (a *Analysis) PluginName(index int) string { return a.plugins[index].Name }

// PluginIndex resolves a plugin name to its Analysis index.
func (a *Analysis) PluginIndex(name string) (int, error) {
	for idx, plugin := range a.plugins {
		if plugin.Name == name {
			return idx, nil
		}
	}
	return -1, fmt.Errorf("netsec: unknown plugin %q", name)
}

// MaskOf translates plugin names into a selection mask.
func (a *Analysis) MaskOf(names []string) (uint32, error) {
	var mask uint32
	for _, name := range names {
		idx, err := a.PluginIndex(name)
		if err != nil {
			return 0, err
		}
		mask |= 1 << uint(idx)
	}
	return mask, nil
}

// PluginNames returns the names selected by mask in Analysis order.
func (a *Analysis) PluginNames(mask uint32) []string {
	names := make([]string, 0, bits.OnesCount32(mask))
	for remainder := mask; remainder != 0; remainder &= remainder - 1 {
		idx := bits.TrailingZeros32(remainder)
		names = append(names, a.plugins[idx].Name)
	}
	return names
}

// key builds the composite label vector of secret s under the selected
// plugins. Equal keys mean membership in the same refinement cell.
func (a *Analysis) key(mask uint32, secret int) string {
	var buffer []byte
	for remainder := mask; remainder != 0; remainder &= remainder - 1 {
		plugin := bits.TrailingZeros32(remainder)
		buffer = strconv.AppendInt(buffer, int64(a.ids[plugin][secret]), 10)
		buffer = append(buffer, ',')
	}
	return string(buffer)
}

// rates computes both identification rates of a combination in one pass,
// grouping secrets by their composite label vector.
func (a *Analysis) rates(mask uint32) (bayes float64, singleton float64) {
	classIndex := make(map[string]int, len(a.secrets))
	masses := make([]float64, 0, len(a.secrets))
	best := make([]float64, 0, len(a.secrets))
	sizes := make([]int, 0, len(a.secrets))

	for secret, weight := range a.weights {
		idx, ok := classIndex[a.key(mask, secret)]
		if !ok {
			idx = len(masses)
			classIndex[a.key(mask, secret)] = idx
			masses = append(masses, 0)
			best = append(best, 0)
			sizes = append(sizes, 0)
		}
		masses[idx] += weight
		sizes[idx]++
		if weight > best[idx] {
			best[idx] = weight
		}
	}

	for idx, mass := range masses {
		bayes += best[idx]
		if sizes[idx] == 1 {
			singleton += mass
		}
	}
	return bayes, singleton
}

// assemble turns member groups into a Partition with masses and guesses.
func (a *Analysis) assemble(mask uint32, groups [][]int) *Partition {
	sort.Slice(groups, func(i, j int) bool { return groups[i][0] < groups[j][0] })

	partition := &Partition{Mask: mask, Classes: make([]Class, 0, len(groups))}
	for _, members := range groups {
		class := Class{
			Members: append([]int(nil), members...),
			Secrets: make([]string, 0, len(members)),
		}
		for _, secret := range members {
			class.Secrets = append(class.Secrets, a.secrets[secret])
			class.Mass += a.weights[secret]
			if a.weights[secret] > class.GuessMass {
				class.GuessMass = a.weights[secret]
				class.Guess = a.secrets[secret]
			}
		}
		partition.Classes = append(partition.Classes, class)
		partition.BayesRate += class.GuessMass
		if len(members) == 1 {
			partition.SingletonRate += class.Mass
		}
	}
	return partition
}

// Refine returns the common refinement of the plugins selected by mask: two
// secrets land in the same Class exactly when every selected plugin emits the
// same label for both.
func (a *Analysis) Refine(mask uint32) *Partition {
	classIndex := make(map[string]int, len(a.secrets))
	groups := make([][]int, 0, len(a.secrets))
	for secret := range a.secrets {
		idx, ok := classIndex[a.key(mask, secret)]
		if !ok {
			idx = len(groups)
			classIndex[a.key(mask, secret)] = idx
			groups = append(groups, nil)
		}
		groups[idx] = append(groups[idx], secret)
	}
	return a.assemble(mask, groups)
}

// RefineNames is Refine with plugin names instead of a mask.
func (a *Analysis) RefineNames(names []string) (*Partition, error) {
	mask, err := a.MaskOf(names)
	if err != nil {
		return nil, err
	}
	return a.Refine(mask), nil
}

// pairwiseRefine builds the same refinement with an independent per-secret
// enumeration: every secret pair is tested for label agreement under each
// selected plugin and merged with a union-find structure.
func (a *Analysis) pairwiseRefine(mask uint32) *Partition {
	parent := make([]int, len(a.secrets))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(node int) int {
		if parent[node] != node {
			parent[node] = find(parent[node])
		}
		return parent[node]
	}
	union := func(x, y int) {
		x, y = find(x), find(y)
		if x != y {
			parent[y] = x
		}
	}

	for left := 0; left < len(a.secrets); left++ {
		for right := left + 1; right < len(a.secrets); right++ {
			indistinguishable := true
			for remainder := mask; remainder != 0; remainder &= remainder - 1 {
				plugin := bits.TrailingZeros32(remainder)
				if a.ids[plugin][left] != a.ids[plugin][right] {
					indistinguishable = false
					break
				}
			}
			if indistinguishable {
				union(left, right)
			}
		}
	}

	rootGroup := map[int]int{}
	groups := make([][]int, 0)
	for secret := range a.secrets {
		root := find(secret)
		idx, ok := rootGroup[root]
		if !ok {
			idx = len(groups)
			rootGroup[root] = idx
			groups = append(groups, nil)
		}
		groups[idx] = append(groups[idx], secret)
	}
	return a.assemble(mask, groups)
}

// reaches reports whether rate meets threshold within floating point tolerance.
func reaches(rate, threshold float64) bool {
	return rate+epsilon >= threshold
}
