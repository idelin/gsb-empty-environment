package netsec

import (
	"fmt"
	"strings"
)

// Experiment is the complete record of one (public input, secret) run.
// Accesses contains prime, victim, and probe accesses in execution order.
// The three phase slices contain the same access values, grouped by phase.
type Experiment struct {
	Public           int
	Secret           int
	VictimAddresses  []int
	Accesses         []AccessResult
	PrimeAccesses    []AccessResult
	VictimAccesses   []AccessResult
	ProbeAccesses    []AccessResult
	ProbeBits        []bool
	ProbeObservation string
	FinalCache       [NumSets]SetState
}

// Witness records two secrets with different probe observations for the same
// public input.
type Witness struct {
	Public       int
	SecretA      int
	SecretB      int
	ObservationA string
	ObservationB string
}

// ObservationString converts hit bits to a compact observation string. Hits
// are 'H' and misses are 'M'.
func ObservationString(bits []bool) string {
	var builder strings.Builder
	builder.Grow(len(bits))
	for _, hit := range bits {
		if hit {
			builder.WriteByte('H')
		} else {
			builder.WriteByte('M')
		}
	}
	return builder.String()
}

// Simulate runs one complete experiment from an empty cache.
func Simulate(public, secret int, prime, probe []int, victim Victim) (Experiment, error) {
	if public < 0 || public >= AddressSpace {
		return Experiment{}, fmt.Errorf("netsec: public value %d outside [0,%d)", public, AddressSpace)
	}
	if secret < 0 || secret >= NumSecrets {
		return Experiment{}, fmt.Errorf("netsec: secret %d outside [0,%d)", secret, NumSecrets)
	}
	if victim == nil {
		return Experiment{}, fmt.Errorf("netsec: nil victim")
	}
	for i, addr := range prime {
		if err := ValidateAddress(addr); err != nil {
			return Experiment{}, fmt.Errorf("netsec: prime[%d]: %w", i, err)
		}
	}
	for i, addr := range probe {
		if err := ValidateAddress(addr); err != nil {
			return Experiment{}, fmt.Errorf("netsec: probe[%d]: %w", i, err)
		}
	}

	victimAddresses, err := victim.LoadAddresses(public, secret)
	if err != nil {
		return Experiment{}, fmt.Errorf("netsec: victim: %w", err)
	}
	if len(victimAddresses) > MaxVictimLoads {
		return Experiment{}, fmt.Errorf("netsec: victim has %d loads, maximum is %d", len(victimAddresses), MaxVictimLoads)
	}
	for i, addr := range victimAddresses {
		if err := ValidateAddress(addr); err != nil {
			return Experiment{}, fmt.Errorf("netsec: victim load %d: %w", i, err)
		}
	}

	experiment := Experiment{
		Public:          public,
		Secret:          secret,
		VictimAddresses: append([]int(nil), victimAddresses...),
	}
	cache := NewCache()

	for i, addr := range prime {
		result, err := cache.access(addr, PhasePrime, i)
		if err != nil {
			return Experiment{}, err
		}
		experiment.Accesses = append(experiment.Accesses, result)
		experiment.PrimeAccesses = append(experiment.PrimeAccesses, result)
	}
	for i, addr := range victimAddresses {
		result, err := cache.access(addr, PhaseVictim, i)
		if err != nil {
			return Experiment{}, err
		}
		experiment.Accesses = append(experiment.Accesses, result)
		experiment.VictimAccesses = append(experiment.VictimAccesses, result)
	}
	for i, addr := range probe {
		result, err := cache.access(addr, PhaseProbe, i)
		if err != nil {
			return Experiment{}, err
		}
		experiment.Accesses = append(experiment.Accesses, result)
		experiment.ProbeAccesses = append(experiment.ProbeAccesses, result)
		experiment.ProbeBits = append(experiment.ProbeBits, result.Hit)
	}
	experiment.ProbeObservation = ObservationString(experiment.ProbeBits)
	experiment.FinalCache = cache.Snapshot()
	return experiment, nil
}

// PublicOutcome holds all experiments and witnesses for one public input.
type PublicOutcome struct {
	Public      int
	Experiments []Experiment
	Witnesses   []Witness
	Partition   ObservationPartition
}

// Verdict is the evidence-based conclusion of a search.
type Verdict uint8

const (
	// VerdictInconclusive means no distinguishing pair was found, but the
	// search did not enumerate every secret in a non-empty public set.
	VerdictInconclusive Verdict = iota
	// VerdictDistinguishable means at least one witness pair was found.
	VerdictDistinguishable
	// VerdictIndistinguishable means every secret 0..15 and every supplied
	// public value was enumerated and no witness pair exists.
	VerdictIndistinguishable
)

func (v Verdict) String() string {
	switch v {
	case VerdictDistinguishable:
		return "distinguishable"
	case VerdictIndistinguishable:
		return "indistinguishable"
	default:
		return "inconclusive"
	}
}

// SearchResult is the full result of enumerating an experiment grid.
type SearchResult struct {
	Publics []PublicOutcome
	Secrets []int
	// MissingSecrets lists 0..15 secrets that were not enumerated.
	MissingSecrets []int
	Witnesses      []Witness
	FirstWitness   *Witness
	Complete       bool
	// Distinguishable only asserts that at least one secret pair differs for
	// one public input; it is valid even for an incomplete sweep.
	Distinguishable bool
	// Indistinguishable is true only when Complete is true and no witness
	// exists. It is relative to the supplied experiment, never an absolute
	// statement about all possible attackers.
	Indistinguishable bool
	Verdict           Verdict
}

// SearchConfig describes a Prime+Probe experiment grid. A nil Secrets slice
// means all secrets 0..15. Publics are deduplicated, secrets are deduplicated,
// and both keep the caller's order.
type SearchConfig struct {
	Publics []int
	Secrets []int
	Prime   []int
	Probe   []int
	Victim  Victim
}

// Search enumerates every requested public value and secret from an empty cache
// for each experiment, then searches for pairs with differing probe
// observations under the same public input.
func Search(config SearchConfig) (*SearchResult, error) {
	if config.Victim == nil {
		return nil, fmt.Errorf("netsec: nil victim")
	}

	publics, err := uniqueValues(config.Publics, "public", AddressSpace)
	if err != nil {
		return nil, err
	}
	secrets, err := normalizedSecrets(config.Secrets)
	if err != nil {
		return nil, err
	}
	for i, addr := range config.Prime {
		if err := ValidateAddress(addr); err != nil {
			return nil, fmt.Errorf("netsec: prime[%d]: %w", i, err)
		}
	}
	for i, addr := range config.Probe {
		if err := ValidateAddress(addr); err != nil {
			return nil, fmt.Errorf("netsec: probe[%d]: %w", i, err)
		}
	}

	result := &SearchResult{
		Secrets: append([]int(nil), secrets...),
	}
	selectedSecrets := make(map[int]bool, len(secrets))
	for _, secret := range secrets {
		selectedSecrets[secret] = true
	}
	for secret := 0; secret < NumSecrets; secret++ {
		if !selectedSecrets[secret] {
			result.MissingSecrets = append(result.MissingSecrets, secret)
		}
	}

	for _, public := range publics {
		outcome := PublicOutcome{Public: public}
		for _, secret := range secrets {
			experiment, err := Simulate(public, secret, config.Prime, config.Probe, config.Victim)
			if err != nil {
				return nil, fmt.Errorf("netsec: public %d secret %d: %w", public, secret, err)
			}
			outcome.Experiments = append(outcome.Experiments, experiment)
		}

		for i := 0; i < len(outcome.Experiments); i++ {
			for j := i + 1; j < len(outcome.Experiments); j++ {
				left := outcome.Experiments[i]
				right := outcome.Experiments[j]
				if left.ProbeObservation == right.ProbeObservation {
					continue
				}
				witness := Witness{
					Public:       public,
					SecretA:      left.Secret,
					SecretB:      right.Secret,
					ObservationA: left.ProbeObservation,
					ObservationB: right.ProbeObservation,
				}
				outcome.Witnesses = append(outcome.Witnesses, witness)
				result.Witnesses = append(result.Witnesses, witness)
			}
		}

		outcome.Partition = buildPartition(public, outcome.Experiments, len(secrets) == NumSecrets)
		result.Publics = append(result.Publics, outcome)
	}

	for i := range result.Witnesses {
		first := result.Witnesses[i]
		result.FirstWitness = &first
		break
	}

	result.Distinguishable = len(result.Witnesses) > 0
	result.Complete = len(result.MissingSecrets) == 0 && len(publics) > 0
	switch {
	case result.Distinguishable:
		result.Verdict = VerdictDistinguishable
	case result.Complete:
		result.Indistinguishable = true
		result.Verdict = VerdictIndistinguishable
	default:
		result.Verdict = VerdictInconclusive
	}
	return result, nil
}

// SearchSecrets is a convenience wrapper for Search with an explicit secret
// list. A nil secret list means all 0..15.
func SearchSecrets(publics, secrets, prime, probe []int, victim Victim) (*SearchResult, error) {
	return Search(SearchConfig{
		Publics: publics,
		Secrets: secrets,
		Prime:   prime,
		Probe:   probe,
		Victim:  victim,
	})
}

// FindDistinguishingWitness returns the first secret pair whose probe
// observations differ under one public input. It returns nil when the search
// finds no pair; callers must inspect the coverage metadata before treating
// that as an indistinguishability claim.
func FindDistinguishingWitness(publics, prime, probe []int, victim Victim) (*Witness, error) {
	result, err := SearchSecrets(publics, nil, prime, probe, victim)
	if err != nil {
		return nil, err
	}
	if result.FirstWitness == nil {
		return nil, nil
	}
	witness := *result.FirstWitness
	return &witness, nil
}

func uniqueValues(values []int, name string, limit int) ([]int, error) {
	out := make([]int, 0, len(values))
	seen := make(map[int]bool, len(values))
	for i, value := range values {
		if value < 0 || value >= limit {
			return nil, fmt.Errorf("netsec: %s[%d]=%d outside [0,%d)", name, i, value, limit)
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out, nil
}

func normalizedSecrets(secrets []int) ([]int, error) {
	if secrets == nil {
		out := make([]int, NumSecrets)
		for i := range out {
			out[i] = i
		}
		return out, nil
	}
	return uniqueValues(secrets, "secret", NumSecrets)
}
