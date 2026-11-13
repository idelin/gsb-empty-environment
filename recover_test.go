package netsec_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"testing"

	netsec "example.org/netsec/q0022"
)

func TestPairwiseCompatibilityDoesNotImplySolution(t *testing.T) {
	records := []netsec.Record{
		{ID: "a", Ciphertext: []byte{0}, Candidates: [][]byte{{0}, {1}}},
		{ID: "b", Ciphertext: []byte{0}, Candidates: [][]byte{{1}, {2}}},
		{ID: "c", Ciphertext: []byte{0}, Candidates: [][]byte{{0}, {2}}},
	}
	got, err := netsec.Recover(records)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != netsec.Unsatisfiable {
		t.Fatalf("status = %s, want %s", got.Status, netsec.Unsatisfiable)
	}
	if got.Conflict == nil || !reflect.DeepEqual(got.Conflict.RecordIDs, []string{"a", "b", "c"}) {
		t.Fatalf("conflict = %#v", got.Conflict)
	}
	for _, subset := range [][]netsec.Record{
		{records[0], records[1]},
		{records[0], records[2]},
		{records[1], records[2]},
	} {
		if status := bruteStatus(subset); status != netsec.Satisfiable {
			t.Fatalf("pair %v should be satisfiable", subset)
		}
	}
	if len(got.Conflict.Deletions) != 3 {
		t.Fatalf("deletions = %d, want 3", len(got.Conflict.Deletions))
	}
	for _, deletion := range got.Conflict.Deletions {
		if !validAssignment(records, deletion.FullIndices) {
			t.Fatalf("invalid deletion witness %#v", deletion)
		}
	}
}

func TestNonOverlappingSegmentsAndTruncation(t *testing.T) {
	records := []netsec.Record{
		{
			ID:         "first",
			Ciphertext: []byte{0, 0},
			Offset:     0,
			Candidates: entries(5, []byte{7, 7}),
		},
		{
			ID:         "second",
			Ciphertext: []byte{0},
			Offset:     10,
			Candidates: entries(5, []byte{9}),
		},
	}
	got, err := netsec.Recover(records)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != netsec.Satisfiable {
		t.Fatalf("status = %s (%#v)", got.Status, got)
	}
	if !got.Truncated {
		t.Fatal("expected witness truncation")
	}
	if len(got.Witnesses) != netsec.MaxWitnesses {
		t.Fatalf("witnesses = %d, want %d", len(got.Witnesses), netsec.MaxWitnesses)
	}
	if !reflect.DeepEqual(got.Candidates[0], []int{0, 1, 2, 3, 4}) ||
		!reflect.DeepEqual(got.Candidates[1], []int{0, 1, 2, 3, 4}) {
		t.Fatalf("candidates = %#v", got.Candidates)
	}
	if !reflect.DeepEqual(got.Determined, map[int]byte{0: 7, 1: 7, 10: 9}) {
		t.Fatalf("determined = %#v", got.Determined)
	}
	for i, witness := range got.Witnesses {
		if witness[0] != i/5 || witness[1] != i%5 {
			t.Fatalf("witness %d = %#v, not lexicographic", i, witness)
		}
	}
	for _, witness := range got.Witnesses {
		if !validAssignment(records, witness) {
			t.Fatalf("invalid witness %#v", witness)
		}
	}
}

func TestEmptyMessagesAndDuplicateCandidates(t *testing.T) {
	records := []netsec.Record{
		{
			ID:         "empty",
			Ciphertext: nil,
			Candidates: [][]byte{{}, {}},
		},
		{
			ID:         "dup",
			Ciphertext: []byte{0, 0},
			Candidates: entries(11, []byte{1, 1}),
		},
	}
	got, err := netsec.Recover(records)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != netsec.Satisfiable {
		t.Fatalf("status = %s", got.Status)
	}
	if !got.Truncated {
		t.Fatal("duplicate-index combinations should exceed the witness limit")
	}
	if !reflect.DeepEqual(got.Candidates[0], []int{0, 1}) {
		t.Fatalf("candidates = %#v", got.Candidates)
	}
	if !reflect.DeepEqual(got.Candidates[1], []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) {
		t.Fatalf("duplicate candidates = %#v", got.Candidates[1])
	}
	if !reflect.DeepEqual(got.Determined, map[int]byte{0: 1, 1: 1}) {
		t.Fatalf("determined = %#v", got.Determined)
	}
	if len(got.Witnesses) != netsec.MaxWitnesses {
		t.Fatalf("witnesses = %d, want %d", len(got.Witnesses), netsec.MaxWitnesses)
	}
	for i, witness := range got.Witnesses {
		if witness[0] != i/11 || witness[1] != i%11 {
			t.Fatalf("witness %d = %#v, not lexicographic", i, witness)
		}
	}
}

func TestUndeterminedBytesAreNotGuessed(t *testing.T) {
	records := []netsec.Record{
		{
			ID:         "secret",
			Ciphertext: []byte{0},
			Candidates: [][]byte{{3}, {5}},
		},
		{
			ID:         "known",
			Ciphertext: []byte{0},
			Offset:     20,
			Candidates: [][]byte{{8}},
		},
	}
	got, err := netsec.Recover(records)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != netsec.Satisfiable {
		t.Fatalf("status = %s", got.Status)
	}
	if _, exists := got.Determined[0]; exists {
		t.Fatalf("position 0 was guessed: %#v", got.Determined)
	}
	if got.Determined[20] != 8 {
		t.Fatalf("position 20 = %#v", got.Determined)
	}
	if len(got.Witnesses) != 2 {
		t.Fatalf("witnesses = %#v", got.Witnesses)
	}
}

func TestPartialOverlapEliminatesCandidateButKeepsDeterminedBytes(t *testing.T) {
	records := []netsec.Record{
		{
			ID:         "long",
			Ciphertext: []byte{0, 0, 0},
			Candidates: [][]byte{{10, 20, 30}},
		},
		{
			ID:         "overlap",
			Ciphertext: []byte{0, 0},
			Offset:     1,
			Candidates: [][]byte{{20, 30}, {20, 31}},
		},
	}
	got, err := netsec.Recover(records)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != netsec.Satisfiable {
		t.Fatalf("status = %s", got.Status)
	}
	if !reflect.DeepEqual(got.Candidates[1], []int{0}) {
		t.Fatalf("candidates = %#v", got.Candidates)
	}
	if !reflect.DeepEqual(got.ExcludedCandidates[1], []int{1}) {
		t.Fatalf("excluded = %#v", got.ExcludedCandidates)
	}
	if !reflect.DeepEqual(got.Determined, map[int]byte{0: 10, 1: 20, 2: 30}) {
		t.Fatalf("determined = %#v", got.Determined)
	}
	if !reflect.DeepEqual(got.Witnesses, [][]int{{0, 0}}) {
		t.Fatalf("witnesses = %#v", got.Witnesses)
	}
}

func TestBudgetExhaustionReturnsUnknown(t *testing.T) {
	records := []netsec.Record{
		{ID: "a", Ciphertext: []byte{0}, Candidates: [][]byte{{0}, {1}}},
		{ID: "b", Ciphertext: []byte{0}, Candidates: [][]byte{{1}, {2}}},
		{ID: "c", Ciphertext: []byte{0}, Candidates: [][]byte{{0}, {2}}},
	}
	got, err := netsec.Recover(records, netsec.Options{MaxSearchNodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != netsec.Unknown {
		t.Fatalf("status = %s, want unknown", got.Status)
	}
	if !got.BudgetExhausted {
		t.Fatal("expected budget exhaustion")
	}
	if len(got.Determined) != 0 {
		t.Fatalf("unknown result claims determined bytes: %#v", got.Determined)
	}
	if got.ProvenUnsatisfiable {
		t.Fatal("one search node should not prove unsatisfiability")
	}
}

func TestSmallestConflictSubsetTieBreak(t *testing.T) {
	records := []netsec.Record{
		{ID: "z", Ciphertext: []byte{0}, Candidates: [][]byte{{1}}},
		{ID: "b", Ciphertext: []byte{0}, Candidates: [][]byte{{1}}},
		{ID: "a", Ciphertext: []byte{0}, Candidates: [][]byte{{0}}},
		{ID: "d", Ciphertext: []byte{0}, Candidates: [][]byte{{4}}},
		{ID: "c", Ciphertext: []byte{0}, Candidates: [][]byte{{3}}},
	}
	got, err := netsec.Recover(records)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != netsec.Unsatisfiable {
		t.Fatalf("status = %s", got.Status)
	}
	if got.Conflict == nil {
		t.Fatal("missing conflict")
	}
	if !reflect.DeepEqual(got.Conflict.RecordIDs, []string{"a", "b"}) {
		t.Fatalf("conflict IDs = %#v, want [a b]", got.Conflict.RecordIDs)
	}
	if !reflect.DeepEqual(got.Conflict.RecordIndices, []int{2, 1}) {
		t.Fatalf("conflict indices = %#v", got.Conflict.RecordIndices)
	}
	for _, deletion := range got.Conflict.Deletions {
		if !validAssignment(records, deletion.FullIndices) {
			t.Fatalf("invalid deletion witness %#v", deletion)
		}
	}
}

func TestEmptyCandidateListIsSingletonConflict(t *testing.T) {
	records := []netsec.Record{
		{ID: "aaa-empty", Ciphertext: []byte{0}, Candidates: nil},
		{ID: "bbb-ok", Ciphertext: []byte{0}, Candidates: [][]byte{{5}}},
	}
	got, err := netsec.Recover(records)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != netsec.Unsatisfiable || got.Conflict == nil {
		t.Fatalf("result = %#v", got)
	}
	if !reflect.DeepEqual(got.Conflict.RecordIDs, []string{"aaa-empty"}) {
		t.Fatalf("conflict = %#v", got.Conflict)
	}
	if len(got.Conflict.Deletions) != 1 {
		t.Fatalf("deletions = %#v", got.Conflict.Deletions)
	}
	deletion := got.Conflict.Deletions[0]
	if !reflect.DeepEqual(deletion.CandidateIndices, []int{0}) {
		t.Fatalf("deletion = %#v", deletion)
	}
	if !validAssignment(records, deletion.FullIndices) {
		t.Fatalf("invalid deletion %#v", deletion)
	}
	if _, ok := got.Determined[0]; ok {
		t.Fatalf("unsatisfiable result determined a byte: %#v", got.Determined)
	}
}

func TestSkipConflictAnalysis(t *testing.T) {
	records := []netsec.Record{
		{ID: "a", Ciphertext: []byte{0}, Candidates: [][]byte{{0}}},
		{ID: "b", Ciphertext: []byte{0}, Candidates: [][]byte{{1}}},
	}
	got, err := netsec.Recover(records, netsec.Options{SkipConflictAnalysis: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != netsec.Unsatisfiable {
		t.Fatalf("status = %s", got.Status)
	}
	if got.Conflict != nil {
		t.Fatalf("conflict analysis was not skipped: %#v", got.Conflict)
	}
}

func TestValidation(t *testing.T) {
	tooMany := make([]netsec.Record, netsec.MaxRecords+1)
	for i := range tooMany {
		tooMany[i] = netsec.Record{ID: fmt.Sprint(i)}
	}
	if _, err := netsec.Recover(tooMany); err == nil {
		t.Fatal("expected too-many-records error")
	}
	if _, err := netsec.Recover([]netsec.Record{{
		ID:         "negative",
		Offset:     -1,
		Ciphertext: []byte{0},
		Candidates: [][]byte{{0}},
	}}); err == nil {
		t.Fatal("expected negative-offset error")
	}
	if _, err := netsec.Recover([]netsec.Record{{
		ID:         "length",
		Ciphertext: []byte{0},
		Candidates: [][]byte{{0, 1}},
	}}); err == nil {
		t.Fatal("expected candidate-length error")
	}
	tooManyCandidates := make([][]byte, netsec.MaxCandidates+1)
	for i := range tooManyCandidates {
		tooManyCandidates[i] = []byte{0}
	}
	if _, err := netsec.Recover([]netsec.Record{{
		ID:         "count",
		Ciphertext: []byte{0},
		Candidates: tooManyCandidates,
	}}); err == nil {
		t.Fatal("expected too-many-candidates error")
	}
}

func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261113))
	for iteration := 0; iteration < 500; iteration++ {
		records := randomRecords(rng)
		got, err := netsec.Recover(records)
		if err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		want := brute(records)
		if got.Status != want.status {
			t.Fatalf("iteration %d: status = %s, want %s; records=%#v result=%#v", iteration, got.Status, want.status, records, got)
		}
		if !reflect.DeepEqual(got.Candidates, want.candidates) {
			t.Fatalf("iteration %d: candidates = %#v, want %#v; records=%#v", iteration, got.Candidates, want.candidates, records)
		}
		if !reflect.DeepEqual(got.Determined, want.determined) {
			t.Fatalf("iteration %d: determined = %#v, want %#v; records=%#v", iteration, got.Determined, want.determined, records)
		}
		if !reflect.DeepEqual(got.Witnesses, want.witnesses) {
			t.Fatalf("iteration %d: witnesses = %#v, want %#v; records=%#v", iteration, got.Witnesses, want.witnesses, records)
		}
		if got.Truncated != want.truncated {
			t.Fatalf("iteration %d: truncated = %v, want %v; records=%#v", iteration, got.Truncated, want.truncated, records)
		}
		if want.status == netsec.Unsatisfiable {
			if got.Conflict == nil {
				t.Fatalf("iteration %d: missing conflict; records=%#v", iteration, records)
			}
			wantConflict := bruteSmallestConflict(records)
			if !reflect.DeepEqual(got.Conflict.RecordIndices, wantConflict) {
				t.Fatalf("iteration %d: conflict = %#v, want %#v; records=%#v", iteration, got.Conflict.RecordIndices, wantConflict, records)
			}
			for _, deletion := range got.Conflict.Deletions {
				if !validAssignment(records, deletion.FullIndices) {
					t.Fatalf("iteration %d: invalid deletion %#v; records=%#v", iteration, deletion, records)
				}
			}
		}
	}
}

type bruteResult struct {
	status     netsec.Status
	candidates [][]int
	determined map[int]byte
	witnesses  [][]int
	truncated  bool
}

func brute(records []netsec.Record) bruteResult {
	result := bruteResult{
		status:     netsec.Unsatisfiable,
		candidates: make([][]int, len(records)),
		determined: make(map[int]byte),
	}
	for _, record := range records {
		if len(record.Candidates) == 0 {
			return result
		}
	}
	values := make(map[int]map[byte]bool)
	witnessCount := 0
	assignment := make([]int, len(records))
	var visit func(int)
	visit = func(index int) {
		if index == len(records) {
			if !validAssignment(records, assignment) {
				return
			}
			result.status = netsec.Satisfiable
			witnessCount++
			if witnessCount <= netsec.MaxWitnesses {
				witness := make([]int, len(assignment))
				copy(witness, assignment)
				result.witnesses = append(result.witnesses, witness)
			} else {
				result.truncated = true
			}
			applySolutionValues(records, assignment, values)
			return
		}
		record := records[index]
		for candidate := range record.Candidates {
			assignment[index] = candidate
			visit(index + 1)
		}
	}
	visit(0)
	if result.status != netsec.Satisfiable {
		result.witnesses = nil
		return result
	}
	for i := range records {
		seen := make(map[int]bool)
		result.candidates[i] = nil
		for _, witness := range allWitnesses(records) {
			seen[witness[i]] = true
		}
		for candidate := range seen {
			result.candidates[i] = append(result.candidates[i], candidate)
		}
		sort.Ints(result.candidates[i])
	}
	for position, possible := range values {
		if len(possible) == 1 {
			for value := range possible {
				result.determined[position] = value
			}
		}
	}
	return result
}

func allWitnesses(records []netsec.Record) [][]int {
	var witnesses [][]int
	assignment := make([]int, len(records))
	var visit func(int)
	visit = func(index int) {
		if index == len(records) {
			if validAssignment(records, assignment) {
				witnesses = append(witnesses, append([]int(nil), assignment...))
			}
			return
		}
		for candidate := range records[index].Candidates {
			assignment[index] = candidate
			visit(index + 1)
		}
	}
	visit(0)
	return witnesses
}

func applySolutionValues(records []netsec.Record, assignment []int, values map[int]map[byte]bool) {
	for i, candidateIndex := range assignment {
		record := records[i]
		candidate := record.Candidates[candidateIndex]
		for offset, plain := range candidate {
			position := record.Offset + offset
			value := record.Ciphertext[offset] ^ plain
			if values[position] == nil {
				values[position] = make(map[byte]bool)
			}
			values[position][value] = true
		}
	}
}

func validAssignment(records []netsec.Record, assignment []int) bool {
	if len(assignment) != len(records) {
		return false
	}
	key := make(map[int]byte)
	for i, candidateIndex := range assignment {
		if candidateIndex < 0 || candidateIndex >= len(records[i].Candidates) {
			continue
		}
		record := records[i]
		candidate := record.Candidates[candidateIndex]
		for offset, plain := range candidate {
			position := record.Offset + offset
			value := record.Ciphertext[offset] ^ plain
			if previous, exists := key[position]; exists && previous != value {
				return false
			}
			key[position] = value
		}
	}
	return true
}

func bruteStatus(records []netsec.Record) netsec.Status {
	if len(allWitnesses(records)) == 0 {
		return netsec.Unsatisfiable
	}
	return netsec.Satisfiable
}

func bruteSmallestConflict(records []netsec.Record) []int {
	order := make([]int, len(records))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		if records[order[a]].ID != records[order[b]].ID {
			return records[order[a]].ID < records[order[b]].ID
		}
		return order[a] < order[b]
	})
	for size := 1; size <= len(records); size++ {
		var combos [][]int
		var visit func([]int, int, int)
		visit = func(current []int, start, remaining int) {
			if remaining == 0 {
				combos = append(combos, append([]int(nil), current...))
				return
			}
			for i := start; i < len(order); i++ {
				visit(append(current, i), i+1, remaining-1)
			}
		}
		visit(nil, 0, size)
		sort.Slice(combos, func(i, j int) bool {
			for x := range combos[i] {
				if combos[i][x] != combos[j][x] {
					return combos[i][x] < combos[j][x]
				}
			}
			return false
		})
		for _, positions := range combos {
			subset := make([]netsec.Record, 0, size)
			for _, position := range positions {
				subset = append(subset, records[order[position]])
			}
			if bruteStatus(subset) == netsec.Unsatisfiable {
				indices := make([]int, 0, size)
				for _, position := range positions {
					indices = append(indices, order[position])
				}
				return indices
			}
		}
	}
	return nil
}

func randomRecords(rng *rand.Rand) []netsec.Record {
	const alphabet = 4
	recordCount := rng.Intn(5)
	records := make([]netsec.Record, recordCount)
	for i := range records {
		records[i].ID = fmt.Sprintf("%c_%d", 'a'+rng.Intn(6), i)
		records[i].Offset = rng.Intn(5)
		length := rng.Intn(4)
		records[i].Ciphertext = make([]byte, length)
		for t := range records[i].Ciphertext {
			records[i].Ciphertext[t] = byte(rng.Intn(alphabet))
		}
		candidateCount := rng.Intn(4)
		records[i].Candidates = make([][]byte, candidateCount)
		for c := range records[i].Candidates {
			candidate := make([]byte, length)
			for t := range candidate {
				candidate[t] = byte(rng.Intn(alphabet))
			}
			records[i].Candidates[c] = candidate
		}
	}
	return records
}

func entries(count int, value []byte) [][]byte {
	out := make([][]byte, count)
	for i := range out {
		out[i] = append([]byte(nil), value...)
	}
	return out
}

func TestDeterminedBytesSorted(t *testing.T) {
	records := []netsec.Record{{
		ID:         "one",
		Ciphertext: []byte{0, 0},
		Candidates: entries(2, []byte{1, 2}),
	}}
	got, err := netsec.Recover(records)
	if err != nil {
		t.Fatal(err)
	}
	want := []netsec.KeyByte{{Offset: 0, Value: 1}, {Offset: 1, Value: 2}}
	if !reflect.DeepEqual(got.DeterminedBytes, want) {
		t.Fatalf("determined bytes = %#v, want %#v", got.DeterminedBytes, want)
	}
	if slices.Equal(got.Witnesses[1], got.Witnesses[0]) {
		t.Fatal("duplicate candidates should produce distinct candidate-index witnesses")
	}
}

func TestMaximumRecordAndCandidateCounts(t *testing.T) {
	records := make([]netsec.Record, netsec.MaxRecords)
	for i := range records {
		records[i] = netsec.Record{
			ID:         fmt.Sprintf("r%02d", i),
			Ciphertext: []byte{0, 0, 0, 0},
			Candidates: entries(netsec.MaxCandidates, []byte{1, 2, 3, 4}),
		}
	}
	got, err := netsec.Recover(records)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != netsec.Satisfiable {
		t.Fatalf("status = %s, nodes = %d, exhausted = %v", got.Status, got.SearchNodes, got.BudgetExhausted)
	}
	if !got.Truncated || len(got.Witnesses) != netsec.MaxWitnesses {
		t.Fatalf("witness output = %d, truncated = %v", len(got.Witnesses), got.Truncated)
	}
	for i := range records {
		if len(got.Candidates[i]) != netsec.MaxCandidates {
			t.Fatalf("record %d candidates = %d", i, len(got.Candidates[i]))
		}
	}
	if !reflect.DeepEqual(got.Determined, map[int]byte{0: 1, 1: 2, 2: 3, 3: 4}) {
		t.Fatalf("determined = %#v", got.Determined)
	}
}

func TestTwelveRecordMinimalConflict(t *testing.T) {
	records := make([]netsec.Record, netsec.MaxRecords)
	for missing := range records {
		values := make([][]byte, 0, netsec.MaxRecords-1)
		for value := 0; value < netsec.MaxRecords; value++ {
			if value != missing {
				values = append(values, []byte{byte(value)})
			}
		}
		records[missing] = netsec.Record{
			ID:         fmt.Sprintf("r%02d", missing),
			Ciphertext: []byte{0},
			Candidates: values,
		}
	}
	got, err := netsec.Recover(records)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != netsec.Unsatisfiable {
		t.Fatalf("status = %s, nodes = %d, exhausted = %v", got.Status, got.SearchNodes, got.BudgetExhausted)
	}
	if got.Conflict == nil || len(got.Conflict.RecordIndices) != netsec.MaxRecords {
		t.Fatalf("conflict = %#v", got.Conflict)
	}
	if len(got.Conflict.Deletions) != netsec.MaxRecords {
		t.Fatalf("deletions = %d, want %d", len(got.Conflict.Deletions), netsec.MaxRecords)
	}
	for _, deletion := range got.Conflict.Deletions {
		if !validAssignment(records, deletion.FullIndices) {
			t.Fatalf("invalid deletion %#v", deletion)
		}
	}
}
