package newsindex_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	newsindex "prescreen.local/ddd-10559-v2"
)

// differentialOp applies one atomic batch. variant 0 and variant 1 must differ
// only in data that is unauthorized for every principal in comparedUsers.
type differentialOp struct {
	name  string
	apply func(archive *newsindex.Archive, variant int) error
}

func applyBatch(archive *newsindex.Archive, build func(*newsindex.Batch) *newsindex.Batch) error {
	_, err := archive.Commit(build(newsindex.NewBatch()))
	return err
}

func differentialOps() []differentialOp {
	return []differentialOp{
		{name: "putA", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Put(newsindex.PublicGroup, "A", "public A")
			})
		}},
		{name: "putB", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Put(newsindex.PublicGroup, "B", "public B")
			})
		}},
		{name: "putC", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Put(team, "C", "team C")
			})
		}},
		{name: "editA", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Put(newsindex.PublicGroup, "A", "public A edited")
			})
		}},
		{name: "removeA", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Remove(newsindex.PublicGroup, "A")
			})
		}},
		{name: "putAPrivate", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Put(team, "A", "team A")
			})
		}},
		{name: "removeAPrivate", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Remove(team, "A")
			})
		}},
		{name: "linkAB", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Link("A", "B")
			})
		}},
		{name: "linkBA", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Link("B", "A")
			})
		}},
		{name: "linkAA", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Link("A", "A")
			})
		}},
		{name: "linkABPublic", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.LinkIn(newsindex.PublicGroup, "A", "B")
			})
		}},
		{name: "linkABTeam", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.LinkIn(team, "A", "B")
			})
		}},
		{name: "unlinkAB", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Unlink("A", "B")
			})
		}},
		{name: "grantBobTeam", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Grant(bob, team)
			})
		}},
		{name: "revokeBobTeam", apply: func(a *newsindex.Archive, _ int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Revoke(bob, team)
			})
		}},
		{name: "hiddenPut", apply: func(a *newsindex.Archive, variant int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Put(vault, "H", fmt.Sprintf("hidden body %d", variant))
			})
		}},
		{name: "hiddenEdit", apply: func(a *newsindex.Archive, variant int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				return b.Put(vault, "H", fmt.Sprintf("hidden edit %d", variant))
			})
		}},
		{name: "hiddenOtherID", apply: func(a *newsindex.Archive, variant int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				id := newsindex.MessageID("I")
				if variant == 1 {
					id = "J"
				}
				return b.Put(vault, id, fmt.Sprintf("hidden other %d", variant))
			})
		}},
		{name: "hiddenRemove", apply: func(a *newsindex.Archive, variant int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				id := newsindex.MessageID("H")
				if variant == 1 {
					id = "J"
				}
				return b.Remove(vault, id)
			})
		}},
		{name: "hiddenLink", apply: func(a *newsindex.Archive, variant int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				child := newsindex.MessageID("A")
				if variant == 1 {
					child = "B"
				}
				return b.Link("H", child)
			})
		}},
		{name: "hiddenACL", apply: func(a *newsindex.Archive, variant int) error {
			return applyBatch(a, func(b *newsindex.Batch) *newsindex.Batch {
				// Both variants grant one empty group, so even a principal
				// that exists only in hidden ACL state has an identical
				// visible view in both archives.
				group := newsindex.Group("unused-a")
				if variant == 1 {
					group = "unused-b"
				}
				return b.Grant("mallory", group)
			})
		}},
	}
}

type getDump struct {
	View newsindex.MessageView
	Err  string
}

type pageDump struct {
	Page newsindex.Page
	Err  string
}

type outputDump struct {
	Pages          []pageDump
	Gets           map[newsindex.MessageID]getDump
	Changes        []newsindex.Notification
	AuthGeneration uint64
}

var comparedUsers = []newsindex.PrincipalID{alice, bob, "mallory"}

var comparedIDs = []newsindex.MessageID{
	"A", "B", "C", "H", "I", "J", "G", "missing",
}

func dumpOutput(t *testing.T, archive *newsindex.Archive, user newsindex.PrincipalID) outputDump {
	t.Helper()
	dump := outputDump{
		Gets:           make(map[newsindex.MessageID]getDump, len(comparedIDs)),
		AuthGeneration: archive.AuthorizationGeneration(user),
	}

	cursor := ""
	for pageNumber := 0; pageNumber < 20; pageNumber++ {
		result, err := archive.Page(user, newsindex.PageRequest{Limit: 2, Cursor: cursor})
		entry := pageDump{Page: result}
		if err != nil {
			entry.Err = err.Error()
		}
		dump.Pages = append(dump.Pages, entry)
		if err != nil || result.Next == "" {
			break
		}
		cursor = result.Next
	}

	for _, id := range comparedIDs {
		view, err := archive.Get(user, id)
		entry := getDump{View: view}
		if err != nil {
			entry.Err = err.Error()
		}
		dump.Gets[id] = entry
	}

	changes, err := archive.Changes(user, 0)
	if err != nil {
		t.Fatalf("Changes(%s): %v", user, err)
	}
	dump.Changes = changes
	return dump
}

func opNames(sequence []differentialOp) string {
	names := make([]string, len(sequence))
	for i, op := range sequence {
		names[i] = op.name
	}
	return strings.Join(names, " > ")
}

func comparePair(t *testing.T, sequence []differentialOp) {
	t.Helper()
	primary := newsindex.New()
	mirror := newsindex.New()
	for step, op := range sequence {
		if err := op.apply(primary, 0); err != nil {
			t.Fatalf("step %d (%s) primary: %v; sequence: %s", step, op.name, err, opNames(sequence))
		}
		if err := op.apply(mirror, 1); err != nil {
			t.Fatalf("step %d (%s) mirror: %v; sequence: %s", step, op.name, err, opNames(sequence))
		}
		for _, user := range comparedUsers {
			left := dumpOutput(t, primary, user)
			right := dumpOutput(t, mirror, user)
			if !reflect.DeepEqual(left, right) {
				t.Fatalf("unauthorized data changed %s output at step %d of %s\nprimary: %#v\nmirror:  %#v",
					user, step, opNames(sequence), left, right)
			}
		}
	}
}

func enumerateSequences(ops []differentialOp, length int, visit func([]differentialOp)) {
	sequence := make([]differentialOp, length)
	var walk func(int)
	walk = func(index int) {
		if index == length {
			visit(sequence)
			return
		}
		for _, op := range ops {
			sequence[index] = op
			walk(index + 1)
		}
	}
	walk(0)
}

func TestDifferentialUnauthorizedDataExhaustiveShort(t *testing.T) {
	ops := differentialOps()
	const length = 3
	count := 0
	enumerateSequences(ops, length, func(sequence []differentialOp) {
		count++
		comparePair(t, sequence)
	})
	if count != len(ops)*len(ops)*len(ops) {
		t.Fatalf("enumerated %d sequences, want %d", count, len(ops)*len(ops)*len(ops))
	}
}

func TestDifferentialUnauthorizedDataExhaustiveLengthFourSubset(t *testing.T) {
	byName := make(map[string]differentialOp)
	for _, op := range differentialOps() {
		byName[op.name] = op
	}
	ops := []differentialOp{
		byName["putA"],
		byName["putC"],
		byName["removeA"],
		byName["linkAB"],
		byName["grantBobTeam"],
		byName["revokeBobTeam"],
		byName["hiddenPut"],
		byName["hiddenRemove"],
	}
	const length = 4
	count := 0
	enumerateSequences(ops, length, func(sequence []differentialOp) {
		count++
		comparePair(t, sequence)
	})
	if count != 4096 {
		t.Fatalf("enumerated %d sequences, want 4096", count)
	}
}

func TestDifferentialUnauthorizedDataRandomLonger(t *testing.T) {
	ops := differentialOps()
	random := rand.New(rand.NewSource(10559))
	for trial := 0; trial < 250; trial++ {
		sequence := make([]differentialOp, 7)
		for i := range sequence {
			sequence[i] = ops[random.Intn(len(ops))]
		}
		comparePair(t, sequence)
	}
}

func TestDifferentialExtraHiddenCommits(t *testing.T) {
	// The mirror archive performs an extra unauthorized commit after every
	// visible batch. Credit for these commits must not appear in any
	// compared principal's pages, lookups, cursor tokens, or notifications.
	visible := []differentialOp{
		differentialOps()[0],  // put A
		differentialOps()[2],  // put C in team
		differentialOps()[5],  // put A in team
		differentialOps()[7],  // link A -> B
		differentialOps()[13], // grant Bob team
		differentialOps()[0],  // put A again (content edit)
		differentialOps()[14], // revoke Bob team
	}
	hidden := []differentialOp{
		differentialOps()[15], // hidden put
		differentialOps()[16], // hidden edit
		differentialOps()[17], // hidden other ID
		differentialOps()[18], // hidden remove
		differentialOps()[19], // hidden link
	}

	primary := newsindex.New()
	mirror := newsindex.New()
	for step, op := range visible {
		if err := op.apply(primary, 0); err != nil {
			t.Fatal(err)
		}
		if err := op.apply(mirror, 1); err != nil {
			t.Fatal(err)
		}
		hiddenOp := hidden[step%len(hidden)]
		if err := hiddenOp.apply(mirror, step%2); err != nil {
			t.Fatal(err)
		}
		for _, user := range comparedUsers {
			left := dumpOutput(t, primary, user)
			right := dumpOutput(t, mirror, user)
			if !reflect.DeepEqual(left, right) {
				t.Fatalf("extra hidden commit changed %s output after step %d (%s)\nprimary: %#v\nmirror:  %#v",
					user, step, op.name, left, right)
			}
		}
	}
}
