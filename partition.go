package netsec

import (
	"fmt"
	"strings"
)

// ObservationClass groups secrets that have exactly the same probe
// observation under one public input.
type ObservationClass struct {
	Observation string
	Secrets     []int
}

// ObservationPartition is the observation-equivalence partition for one
// public input.
type ObservationPartition struct {
	Public   int
	Classes  []ObservationClass
	Secrets  []int
	Complete bool
}

// Class returns the equivalence class for an observation.
func (p ObservationPartition) Class(observation string) (ObservationClass, bool) {
	for _, class := range p.Classes {
		if class.Observation == observation {
			return class, true
		}
	}
	return ObservationClass{}, false
}

// Candidates returns the secrets in the class for an observation. A nil result
// means the observation was not produced by any enumerated secret.
func (p ObservationPartition) Candidates(observation string) []int {
	class, ok := p.Class(observation)
	if !ok {
		return nil
	}
	return append([]int(nil), class.Secrets...)
}

// Recover returns a secret only when the partition is complete and the
// observation class contains exactly one secret. Equal traces are deliberately
// kept in one class instead of being treated as distinct secrets.
func (p ObservationPartition) Recover(observation string) (int, bool) {
	class, ok := p.Class(observation)
	if !ok || !p.Complete || len(class.Secrets) != 1 {
		return 0, false
	}
	return class.Secrets[0], true
}

func buildPartition(public int, experiments []Experiment, complete bool) ObservationPartition {
	partition := ObservationPartition{
		Public:   public,
		Complete: complete,
	}
	classByObservation := make(map[string]int)
	for _, experiment := range experiments {
		index, ok := classByObservation[experiment.ProbeObservation]
		if !ok {
			index = len(partition.Classes)
			classByObservation[experiment.ProbeObservation] = index
			partition.Classes = append(partition.Classes, ObservationClass{
				Observation: experiment.ProbeObservation,
			})
		}
		partition.Classes[index].Secrets = append(partition.Classes[index].Secrets, experiment.Secret)
		partition.Secrets = append(partition.Secrets, experiment.Secret)
	}
	return partition
}

// PartitionForPublic returns the observation partition for a public value in
// an existing search result.
func PartitionForPublic(result *SearchResult, public int) (ObservationPartition, error) {
	if result == nil {
		return ObservationPartition{}, fmt.Errorf("netsec: nil search result")
	}
	for _, outcome := range result.Publics {
		if outcome.Public == public {
			return outcome.Partition, nil
		}
	}
	return ObservationPartition{}, fmt.Errorf("netsec: public %d was not enumerated", public)
}

// SecretClass groups secrets whose complete per-public observation vectors are
// identical.
type SecretClass struct {
	Observations []string
	Signature    string
	Secrets      []int
}

// SecretPartition is the observation-equivalence partition across all
// enumerated public values.
type SecretPartition struct {
	Publics  []int
	Classes  []SecretClass
	Secrets  []int
	Complete bool
}

// PartitionSecrets groups each secret by its observation vector over the
// enumerated public values. Secrets with equal traces stay together; different
// traces create different classes.
func PartitionSecrets(result *SearchResult) (SecretPartition, error) {
	if result == nil {
		return SecretPartition{}, fmt.Errorf("netsec: nil search result")
	}

	partition := SecretPartition{
		Complete: result.Complete,
	}
	for _, outcome := range result.Publics {
		partition.Publics = append(partition.Publics, outcome.Public)
	}

	experimentsBySecret := make(map[int][]string)
	for _, outcome := range result.Publics {
		for _, experiment := range outcome.Experiments {
			experimentsBySecret[experiment.Secret] = append(experimentsBySecret[experiment.Secret],
				experiment.ProbeObservation)
		}
	}

	classBySignature := make(map[string]int)
	for _, secret := range result.Secrets {
		observations := experimentsBySecret[secret]
		signature := strings.Join(observations, "|")
		index, ok := classBySignature[signature]
		if !ok {
			index = len(partition.Classes)
			classBySignature[signature] = index
			partition.Classes = append(partition.Classes, SecretClass{
				Observations: append([]string(nil), observations...),
				Signature:    signature,
			})
		}
		partition.Classes[index].Secrets = append(partition.Classes[index].Secrets, secret)
		partition.Secrets = append(partition.Secrets, secret)
	}
	return partition, nil
}

// Recover returns a secret only when the complete secret partition has exactly
// one class containing exactly one secret.
func (p SecretPartition) Recover() (int, bool) {
	if !p.Complete || len(p.Classes) != 1 || len(p.Classes[0].Secrets) != 1 {
		return 0, false
	}
	return p.Classes[0].Secrets[0], true
}

// RecoverFromObservations finds a class by its per-public observation vector,
// then applies the same uniqueness rule as Recover.
func (p SecretPartition) RecoverFromObservations(observations []string) (int, bool) {
	if !p.Complete || len(observations) != len(p.Publics) {
		return 0, false
	}
	signature := strings.Join(observations, "|")
	for _, class := range p.Classes {
		if class.Signature == signature && len(class.Secrets) == 1 {
			return class.Secrets[0], true
		}
	}
	return 0, false
}

// PartitionBySecret is a method form of PartitionSecrets.
func (r *SearchResult) PartitionBySecret() (SecretPartition, error) {
	return PartitionSecrets(r)
}

// Recovery is an explicit candidate set for one probe observation. It is the
// only supported way to turn traces into a secret claim.
type Recovery struct {
	Public      int
	Observation string
	Candidates  []int
	Unique      bool
	Complete    bool
}

// RecoverObservation returns every secret consistent with an observation. It
// reports Unique only when a complete sweep leaves exactly one candidate.
func (r *SearchResult) RecoverObservation(public int, observation string) (Recovery, error) {
	partition, err := PartitionForPublic(r, public)
	if err != nil {
		return Recovery{}, err
	}
	candidates := partition.Candidates(observation)
	unique := partition.Complete && len(candidates) == 1
	return Recovery{
		Public:      public,
		Observation: observation,
		Candidates:  candidates,
		Unique:      unique,
		Complete:    partition.Complete,
	}, nil
}
