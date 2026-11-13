package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// MaxObservations is the largest observation set accepted by Recover.
const MaxObservations = 8

// Observation is one correct/faulty four-byte output pair.
type Observation struct {
	C [KeyBytes]byte `json:"c"`
	F [KeyBytes]byte `json:"f"`
}

// Witness identifies a fault location and nonzero fault value that explains
// one observation for a candidate key.
type Witness struct {
	J uint8 `json:"j"`
	D byte  `json:"d"`
}

// Candidate is a key segment together with one witness for every supplied
// observation, in observation order.
type Candidate struct {
	Key       [KeyBytes]byte `json:"key"`
	Witnesses []Witness      `json:"witnesses"`
}

// Status describes how the observation set constrains the key segment.
type Status string

const (
	// StatusInconsistent means no key in the supplied domain satisfies the model.
	StatusInconsistent Status = "model_inconsistent"
	// StatusUnique means exactly one key candidate remains under the model.
	StatusUnique Status = "unique_under_model"
	// StatusNotUnique means more than one key candidate remains.
	StatusNotUnique Status = "not_unique"
)

// Result is the complete candidate set for the supplied observations and domain.
type Result struct {
	Status     Status      `json:"status"`
	Candidates []Candidate `json:"candidates"`
}

// Domain optionally restricts the allowed values of each key byte. A nil slice
// for a position means the full byte range 0..255; a non-nil empty slice allows
// no value at that position. Domain{} therefore requests the full byte domain.
type Domain [KeyBytes][]byte

// FullDomain returns an unrestricted per-byte key domain.
func FullDomain() Domain {
	return Domain{}
}

// Recover searches the full per-byte key domain.
func Recover(observations []Observation) (Result, error) {
	return RecoverWithDomain(observations, FullDomain())
}

// RecoverWithDomain searches a host-supplied per-byte key domain.
func RecoverWithDomain(observations []Observation, domain Domain) (Result, error) {
	if len(observations) == 0 {
		return Result{}, errors.New("at least one observation is required")
	}
	if len(observations) > MaxObservations {
		return Result{}, fmt.Errorf("at most %d observations are accepted", MaxObservations)
	}

	allowed := normalizeDomain(domain)
	initial := candidatesForObservation(observations[0], allowed)
	survivors := make(map[[KeyBytes]byte][]Witness, len(initial))
	for key, witness := range initial {
		survivors[key] = []Witness{witness}
	}
	for _, observation := range observations[1:] {
		if len(survivors) == 0 {
			break
		}
		observed := candidatesForObservation(observation, allowed)
		next := make(map[[KeyBytes]byte][]Witness, len(survivors))
		for key, witnesses := range survivors {
			witness, ok := observed[key]
			if !ok {
				continue
			}
			extended := make([]Witness, len(witnesses)+1)
			copy(extended, witnesses)
			extended[len(witnesses)] = witness
			next[key] = extended
		}
		survivors = next
	}

	if len(survivors) == 0 {
		return Result{Status: StatusInconsistent}, nil
	}

	keys := make([][KeyBytes]byte, 0, len(survivors))
	for key := range survivors {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return lessKey(keys[i], keys[j])
	})

	candidates := make([]Candidate, len(keys))
	for i, key := range keys {
		candidates[i] = Candidate{Key: key, Witnesses: survivors[key]}
	}
	status := StatusUnique
	if len(candidates) > 1 {
		status = StatusNotUnique
	}
	return Result{Status: status, Candidates: candidates}, nil
}

func normalizeDomain(domain Domain) [KeyBytes][256]bool {
	var allowed [KeyBytes][256]bool
	for position := 0; position < KeyBytes; position++ {
		if domain[position] == nil {
			for value := 0; value < 256; value++ {
				allowed[position][value] = true
			}
			continue
		}
		for _, value := range domain[position] {
			allowed[position][value] = true
		}
	}
	return allowed
}

func candidatesForObservation(observation Observation, allowed [KeyBytes][256]bool) map[[KeyBytes]byte]Witness {
	candidates := make(map[[KeyBytes]byte]Witness)
	var outputDifference [KeyBytes]byte
	for position := 0; position < KeyBytes; position++ {
		outputDifference[position] = observation.C[position] ^ observation.F[position]
	}

	for j := 0; j < KeyBytes; j++ {
		for d := 1; d < 256; d++ {
			var choices [KeyBytes][]byte
			possible := true
			for position := 0; position < KeyBytes; position++ {
				inputDifference := gfMul(faultMatrix[position][j], byte(d))
				values := inverseDifference[outputDifference[position]][inputDifference]
				for _, value := range values {
					keyByte := value ^ observation.C[position]
					if allowed[position][keyByte] {
						choices[position] = append(choices[position], keyByte)
					}
				}
				if len(choices[position]) == 0 {
					possible = false
					break
				}
			}
			if possible {
				enumerateChoices(choices, func(key [KeyBytes]byte) {
					if _, exists := candidates[key]; !exists {
						candidates[key] = Witness{J: uint8(j), D: byte(d)}
					}
				})
			}
		}
	}
	return candidates
}

func enumerateChoices(choices [KeyBytes][]byte, emit func([KeyBytes]byte)) {
	var key [KeyBytes]byte
	var visit func(int)
	visit = func(position int) {
		if position == KeyBytes {
			emit(key)
			return
		}
		for _, value := range choices[position] {
			key[position] = value
			visit(position + 1)
		}
	}
	visit(0)
}

func lessKey(a, b [KeyBytes]byte) bool {
	for i := 0; i < KeyBytes; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
