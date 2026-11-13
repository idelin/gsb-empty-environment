package netsec

import (
	"slices"
	"sort"
	"testing"
)

var knownKey = [KeyBytes]byte{0x2b, 0x7e, 0x15, 0x16}

var uniqueObservations = []Observation{
	{C: [KeyBytes]byte{0xdb, 0x5b, 0x98, 0xa9}, F: [KeyBytes]byte{0x86, 0x18, 0xb9, 0x19}},
	{C: [KeyBytes]byte{0x41, 0xeb, 0x4d, 0x92}, F: [KeyBytes]byte{0xa7, 0xe7, 0x72, 0x7b}},
	{C: [KeyBytes]byte{0x1c, 0xcc, 0xd8, 0xd4}, F: [KeyBytes]byte{0x74, 0x41, 0x01, 0x81}},
	{C: [KeyBytes]byte{0x82, 0xd3, 0x65, 0xb1}, F: [KeyBytes]byte{0x53, 0xab, 0x3e, 0x16}},
	{C: [KeyBytes]byte{0x96, 0xe4, 0x3e, 0x8a}, F: [KeyBytes]byte{0x7e, 0xc5, 0x8d, 0xf0}},
	{C: [KeyBytes]byte{0x46, 0x9a, 0xff, 0x8a}, F: [KeyBytes]byte{0xef, 0x54, 0x57, 0x14}},
	{C: [KeyBytes]byte{0x86, 0x7c, 0x3d, 0x28}, F: [KeyBytes]byte{0x90, 0xaa, 0x4b, 0x0d}},
	{C: [KeyBytes]byte{0x3f, 0x18, 0x8c, 0xc9}, F: [KeyBytes]byte{0x01, 0xbf, 0xfc, 0x1b}},
}

func TestSBoxAndFieldVectors(t *testing.T) {
	tests := []struct {
		input byte
		want  byte
	}{
		{0x00, 0x63},
		{0x01, 0x7c},
		{0x53, 0xed},
		{0xff, 0x16},
	}
	for _, test := range tests {
		if got := SBox(test.input); got != test.want {
			t.Fatalf("SBox(%02x) = %02x, want %02x", test.input, got, test.want)
		}
	}
	for value := 0; value < 256; value++ {
		if got := InvSBox(SBox(byte(value))); got != byte(value) {
			t.Fatalf("InvSBox(SBox(%02x)) = %02x", value, got)
		}
	}
	if got := GFMultiply(0x57, 0x13); got != 0xfe {
		t.Fatalf("GFMultiply(57, 13) = %02x, want fe", got)
	}
	if got := GFMultiply(0x02, 0x80); got != 0x1b {
		t.Fatalf("GFMultiply(02, 80) = %02x, want 1b", got)
	}
}

func TestRecoverKnownKeyUnique(t *testing.T) {
	result, err := Recover(uniqueObservations)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusUnique {
		t.Fatalf("status = %q, candidates = %d, want unique", result.Status, len(result.Candidates))
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Key != knownKey {
		t.Fatalf("candidates = %v, want only %x", result.Candidates, knownKey)
	}
	validateCandidate(t, uniqueObservations, result.Candidates[0])
}

func TestRecoverMultipleCandidatesIsNotUnique(t *testing.T) {
	observations := uniqueObservations[:1]
	result, err := Recover(observations)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusNotUnique {
		t.Fatalf("status = %q, candidates = %d, want not unique", result.Status, len(result.Candidates))
	}
	if len(result.Candidates) <= 1 {
		t.Fatalf("got %d candidates, want multiple", len(result.Candidates))
	}
	found := false
	for _, candidate := range result.Candidates {
		validateCandidate(t, observations, candidate)
		if candidate.Key == knownKey {
			found = true
		}
	}
	if !found {
		t.Fatalf("known key %x is missing from candidates", knownKey)
	}
	for i := 1; i < len(result.Candidates); i++ {
		if !lessKey(result.Candidates[i-1].Key, result.Candidates[i].Key) {
			t.Fatalf("candidates are not in byte order at positions %d and %d", i-1, i)
		}
	}
}

func TestRecoverRejectsZeroFault(t *testing.T) {
	zeroFault := make([]Observation, 4)
	for i := range zeroFault {
		zeroFault[i] = Observation{
			C: [KeyBytes]byte{0x10, 0x20, 0x30, 0x40},
			F: [KeyBytes]byte{0x10, 0x20, 0x30, 0x40},
		}
	}
	result, err := Recover(zeroFault)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusInconsistent || len(result.Candidates) != 0 {
		t.Fatalf("status = %q, candidates = %d, want inconsistent", result.Status, len(result.Candidates))
	}
}

func TestRecoverRejectsSingleByteCorruption(t *testing.T) {
	observations := append([]Observation(nil), uniqueObservations...)
	observations[3].F[0] ^= 0x01
	result, err := Recover(observations)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusInconsistent || len(result.Candidates) != 0 {
		t.Fatalf("status = %q, candidates = %d, want inconsistent", result.Status, len(result.Candidates))
	}
}

func TestRecoverRejectsSingleByteOnlyFault(t *testing.T) {
	observation := Observation{
		C: [KeyBytes]byte{0x10, 0x20, 0x30, 0x40},
		F: [KeyBytes]byte{0x10, 0x21, 0x30, 0x40},
	}
	result, err := Recover([]Observation{observation})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusInconsistent || len(result.Candidates) != 0 {
		t.Fatalf("status = %q, candidates = %d, want inconsistent", result.Status, len(result.Candidates))
	}
}

func TestRecoverSynthesizedKnownKey(t *testing.T) {
	values := [][KeyBytes]byte{
		{0x00, 0x11, 0x22, 0x33},
		{0x01, 0x23, 0x45, 0x67},
		{0x89, 0xab, 0xcd, 0xef},
		{0x12, 0x34, 0x56, 0x78},
		{0xff, 0xee, 0xdd, 0xcc},
		{0xde, 0xad, 0xbe, 0xef},
		{0xca, 0xfe, 0xba, 0xbe},
		{0x0f, 0x1e, 0x2d, 0x3c},
	}
	faultLocations := []uint8{2, 0, 3, 1, 2, 0, 3, 1}
	faultValues := []byte{1, 2, 3, 4, 0x55, 0x80, 0xfe, 0xff}

	observations := make([]Observation, len(values))
	for i, value := range values {
		observations[i] = makeObservation(knownKey, value, faultLocations[i], faultValues[i])
	}
	result, err := Recover(observations)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusUnique || len(result.Candidates) != 1 || result.Candidates[0].Key != knownKey {
		t.Fatalf("status = %q, candidates = %v, want unique %x", result.Status, candidateKeys(result), knownKey)
	}
	validateCandidate(t, observations, result.Candidates[0])
}

func TestRepeatedObservationsPreserveCandidateSet(t *testing.T) {
	once, err := Recover(uniqueObservations[:1])
	if err != nil {
		t.Fatal(err)
	}
	repeatedObservations := make([]Observation, MaxObservations)
	for i := range repeatedObservations {
		repeatedObservations[i] = uniqueObservations[0]
	}
	repeated, err := Recover(repeatedObservations)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Status != once.Status || !slices.Equal(candidateKeys(repeated), candidateKeys(once)) {
		t.Fatalf("repeated candidate set differs from one observation")
	}
	for _, candidate := range repeated.Candidates {
		if len(candidate.Witnesses) != len(repeatedObservations) {
			t.Fatalf("witness count = %d, want %d", len(candidate.Witnesses), len(repeatedObservations))
		}
		validateCandidate(t, repeatedObservations, candidate)
	}
}

func TestSmallDomainMatchesExhaustiveEnumeration(t *testing.T) {
	corrupted := append([]Observation(nil), uniqueObservations...)
	corrupted[6].F[2] ^= 0x80
	repeated := []Observation{
		uniqueObservations[0],
		uniqueObservations[0],
		uniqueObservations[1],
	}
	tests := []struct {
		name         string
		observations []Observation
	}{
		{"unique", uniqueObservations},
		{"single-observation", uniqueObservations[:1]},
		{"repeated", repeated},
		{"corrupted", corrupted},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			domain := fourValueDomain(knownKey)
			result, err := RecoverWithDomain(test.observations, domain)
			if err != nil {
				t.Fatal(err)
			}
			want := exhaustiveCandidates(test.observations, domain)
			got := candidateKeys(result)
			if !slices.Equal(got, want) {
				t.Fatalf("recovered keys = %v, exhaustive keys = %v", got, want)
			}
			if test.name == "unique" && len(want) != 1 {
				t.Fatalf("unique case produced %d exhaustive candidates", len(want))
			}
			if test.name == "corrupted" && len(want) != 0 {
				t.Fatalf("corrupted case produced %d exhaustive candidates", len(want))
			}
			wantStatus := StatusInconsistent
			if len(want) == 1 {
				wantStatus = StatusUnique
			} else if len(want) > 1 {
				wantStatus = StatusNotUnique
			}
			if result.Status != wantStatus {
				t.Fatalf("status = %q, want %q", result.Status, wantStatus)
			}
			for _, candidate := range result.Candidates {
				validateCandidate(t, test.observations, candidate)
			}
		})
	}
}

func TestRecoverInputValidationAndEmptyDomain(t *testing.T) {
	if _, err := Recover(nil); err == nil {
		t.Fatal("Recover(nil) unexpectedly succeeded")
	}
	tooMany := make([]Observation, MaxObservations+1)
	if _, err := Recover(tooMany); err == nil {
		t.Fatal("Recover with too many observations unexpectedly succeeded")
	}
	domain := FullDomain()
	domain[2] = []byte{}
	result, err := RecoverWithDomain(uniqueObservations, domain)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusInconsistent || len(result.Candidates) != 0 {
		t.Fatalf("status = %q, candidates = %d, want inconsistent", result.Status, len(result.Candidates))
	}
}

func TestDefaultDomainMatchesExplicitFullDomain(t *testing.T) {
	implicit, err := Recover(uniqueObservations)
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := RecoverWithDomain(uniqueObservations, FullDomain())
	if err != nil {
		t.Fatal(err)
	}
	if implicit.Status != explicit.Status || !slices.Equal(candidateKeys(implicit), candidateKeys(explicit)) {
		t.Fatalf("default and explicit full domains produced different candidates")
	}
}

func fourValueDomain(key [KeyBytes]byte) Domain {
	var domain Domain
	for i := range domain {
		domain[i] = []byte{key[i], key[i] ^ 0x01, key[i] ^ 0x55, key[i] ^ 0xff}
	}
	return domain
}

func exhaustiveCandidates(observations []Observation, domain Domain) [][KeyBytes]byte {
	values := make([][]byte, KeyBytes)
	for position := 0; position < KeyBytes; position++ {
		seen := [256]bool{}
		for _, value := range domain[position] {
			if !seen[value] {
				seen[value] = true
				values[position] = append(values[position], value)
			}
		}
		sort.Slice(values[position], func(i, j int) bool {
			return values[position][i] < values[position][j]
		})
	}

	var candidates [][KeyBytes]byte
	var key [KeyBytes]byte
	var visit func(int)
	visit = func(position int) {
		if position == KeyBytes {
			for _, observation := range observations {
				if _, ok := explainObservation(observation, key); !ok {
					return
				}
			}
			candidates = append(candidates, key)
			return
		}
		for _, value := range values[position] {
			key[position] = value
			visit(position + 1)
		}
	}
	visit(0)
	return candidates
}

func explainObservation(observation Observation, key [KeyBytes]byte) (Witness, bool) {
	for j := 0; j < KeyBytes; j++ {
		for d := 1; d < 256; d++ {
			valid := true
			for position := 0; position < KeyBytes; position++ {
				value := InvSBox(observation.C[position] ^ key[position])
				faulted := value ^ gfMul(faultMatrix[position][j], byte(d))
				if SBox(faulted) != observation.F[position]^key[position] {
					valid = false
					break
				}
			}
			if valid {
				return Witness{J: uint8(j), D: byte(d)}, true
			}
		}
	}
	return Witness{}, false
}

func validateCandidate(t *testing.T, observations []Observation, candidate Candidate) {
	t.Helper()
	if len(candidate.Witnesses) != len(observations) {
		t.Fatalf("key %x has %d witnesses for %d observations", candidate.Key, len(candidate.Witnesses), len(observations))
	}
	for i, observation := range observations {
		witness := candidate.Witnesses[i]
		if witness.J >= KeyBytes || witness.D == 0 {
			t.Fatalf("invalid witness %+v for key %x", witness, candidate.Key)
		}
		for position := 0; position < KeyBytes; position++ {
			value := InvSBox(observation.C[position] ^ candidate.Key[position])
			faulted := value ^ gfMul(faultMatrix[position][int(witness.J)], witness.D)
			if SBox(faulted) != observation.F[position]^candidate.Key[position] {
				t.Fatalf("witness %+v does not explain observation %d for key %x", witness, i, candidate.Key)
			}
		}
	}
}

func makeObservation(key, value [KeyBytes]byte, j uint8, d byte) Observation {
	var observation Observation
	for position := 0; position < KeyBytes; position++ {
		observation.C[position] = SBox(value[position]) ^ key[position]
		faulted := value[position] ^ gfMul(faultMatrix[position][int(j)], d)
		observation.F[position] = SBox(faulted) ^ key[position]
	}
	return observation
}

func candidateKeys(result Result) [][KeyBytes]byte {
	keys := make([][KeyBytes]byte, len(result.Candidates))
	for i, candidate := range result.Candidates {
		keys[i] = candidate.Key
	}
	return keys
}
