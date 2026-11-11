package secdesc

import (
	"errors"
	"runtime"
	"sync"
	"testing"
)

const (
	rightRead   uint32 = 1
	rightWrite  uint32 = 2
	rightDelete uint32 = 4
	rightAll           = rightRead | rightWrite | rightDelete
)

func allow(trustee string, mask uint32, flags uint8) ACE {
	return ACE{Trustee: trustee, Type: ACEAllow, Mask: mask, Flags: flags}
}

func deny(trustee string, mask uint32, flags uint8) ACE {
	return ACE{Trustee: trustee, Type: ACEDeny, Mask: mask, Flags: flags}
}

func findACE(t *testing.T, aces []ACE, trustee string) ACE {
	t.Helper()
	for _, ace := range aces {
		if ace.Trustee == trustee {
			return ace
		}
	}
	t.Fatalf("ACE for trustee %q not found in %+v", trustee, aces)
	return ACE{}
}

func hasTrustee(aces []ACE, trustee string) bool {
	for _, ace := range aces {
		if ace.Trustee == trustee {
			return true
		}
	}
	return false
}

func trusteeOrder(aces []ACE) []string {
	out := make([]string, len(aces))
	for i, ace := range aces {
		out[i] = ace.Trustee
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func aceEqual(a, b []ACE) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// buildMixedTree constructs a three-level tree of mixed directories and files:
//
//	root (dir)
//	├── dirA (dir)
//	│   ├── fileB (file)
//	│   └── dirB (dir, protected: inheritance is broken here)
//	│       ├── fileE (file)
//	│       └── dirC (dir)
//	└── dirD (dir)
//	    └── fileD (file)
func buildMixedTree(t *testing.T) *Tree {
	t.Helper()
	tree := NewTree()

	if _, err := tree.Add(RootID, RootID, KindContainer, SecurityDescriptor{
		ExplicitACEs: []ACE{
			allow("alice", rightRead, FlagContainerInherit|FlagObjectInherit),
			allow("bob", rightWrite, FlagObjectInherit|FlagInheritOnly),
			allow("carol", rightDelete, FlagContainerInherit|FlagNoPropagate),
		},
	}); err != nil {
		t.Fatalf("add root: %v", err)
	}

	if _, err := tree.Add("dirA", RootID, KindContainer, SecurityDescriptor{
		ExplicitACEs: []ACE{
			deny("mallory", rightAll, 0),
			allow("oscar", rightRead, FlagContainerInherit|FlagObjectInherit),
		},
	}); err != nil {
		t.Fatalf("add dirA: %v", err)
	}
	if _, err := tree.Add("fileB", "dirA", KindFile, SecurityDescriptor{}); err != nil {
		t.Fatalf("add fileB: %v", err)
	}
	if _, err := tree.Add("dirB", "dirA", KindContainer, SecurityDescriptor{
		Protected: true,
		ExplicitACEs: []ACE{
			allow("bob", rightRead, FlagContainerInherit|FlagObjectInherit),
			deny("mallory", rightWrite, FlagContainerInherit|FlagObjectInherit),
		},
	}); err != nil {
		t.Fatalf("add dirB: %v", err)
	}
	if _, err := tree.Add("fileE", "dirB", KindFile, SecurityDescriptor{}); err != nil {
		t.Fatalf("add fileE: %v", err)
	}
	if _, err := tree.Add("dirC", "dirB", KindContainer, SecurityDescriptor{
		ExplicitACEs: []ACE{allow("nina", rightRead, 0)},
	}); err != nil {
		t.Fatalf("add dirC: %v", err)
	}
	if _, err := tree.Add("dirD", RootID, KindContainer, SecurityDescriptor{
		ExplicitACEs: []ACE{
			allow("erin", rightRead, FlagContainerInherit|FlagObjectInherit),
		},
	}); err != nil {
		t.Fatalf("add dirD: %v", err)
	}
	if _, err := tree.Add("fileD", "dirD", KindFile, SecurityDescriptor{}); err != nil {
		t.Fatalf("add fileD: %v", err)
	}

	return tree
}

func TestInitialInheritance(t *testing.T) {
	tree := buildMixedTree(t)
	snap := tree.Current()
	if snap.Version() != 8 {
		t.Fatalf("version after building an 8-node tree = %d, want 8", snap.Version())
	}

	rootACEs, err := snap.EffectiveACEs(RootID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trusteeOrder(rootACEs), []string{"alice", "bob", "carol"}; !equal(got, want) {
		t.Fatalf("root ACE order = %v, want %v", got, want)
	}
	if findACE(t, rootACEs, "bob").AppliesToSelf() {
		t.Fatal("IO entry on root must not apply to the root itself")
	}

	// dirA: explicit entries first, then inherited entries in the order the
	// root enumerates them. The files-only bob still traverses directories
	// because it is marked inherit-only (OI|IO), like Windows semantics.
	dirA, err := snap.EffectiveACEs("dirA")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trusteeOrder(dirA), []string{"mallory", "oscar", "alice", "bob", "carol"}; !equal(got, want) {
		t.Fatalf("dirA ACE order = %v, want %v", got, want)
	}
	mallory := findACE(t, dirA, "mallory")
	if mallory.Type != ACEDeny || mallory.IsInherited() {
		t.Fatalf("explicit deny altered: %+v", mallory)
	}
	carol := findACE(t, dirA, "carol")
	if !carol.IsInherited() || carol.Flags != FlagInherited {
		t.Fatalf("NP entry must reach the direct child with inheritance flags stripped: %+v", carol)
	}

	// fileB: files inherit alice and the files-only bob; container-only
	// carol never reaches files.
	fileB, err := snap.EffectiveACEs("fileB")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trusteeOrder(fileB), []string{"oscar", "alice", "bob"}; !equal(got, want) {
		t.Fatalf("fileB ACE order = %v, want %v", got, want)
	}
	bob := findACE(t, fileB, "bob")
	if !bob.IsInherited() || bob.Flags != FlagInherited {
		t.Fatalf("inherited file ACE must carry only FlagInherited: %+v", bob)
	}

	// dirB is protected: nothing from dirA or the root leaks in, explicit
	// entries keep their declared order.
	dirB, err := snap.EffectiveACEs("dirB")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trusteeOrder(dirB), []string{"bob", "mallory"}; !equal(got, want) {
		t.Fatalf("protected dirB ACE order = %v, want %v", got, want)
	}
	for _, ace := range dirB {
		if ace.IsInherited() {
			t.Fatalf("protected subtree leaked inherited ACE: %+v", ace)
		}
	}

	// Descendants of the protected node inherit its explicit entries, but
	// still cannot receive anything from above the protection boundary.
	fileE, err := snap.EffectiveACEs("fileE")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trusteeOrder(fileE), []string{"bob", "mallory"}; !equal(got, want) {
		t.Fatalf("fileE ACE order = %v, want %v", got, want)
	}
	bobE := findACE(t, fileE, "bob")
	if !bobE.IsInherited() || bobE.Flags != FlagInherited {
		t.Fatalf("protected subtree child must hold an inherited copy: %+v", bobE)
	}
	dirC, err := snap.EffectiveACEs("dirC")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trusteeOrder(dirC), []string{"nina", "bob", "mallory"}; !equal(got, want) {
		t.Fatalf("dirC ACE order = %v, want %v", got, want)
	}
	if hasTrustee(dirC, "carol") || hasTrustee(fileE, "carol") {
		t.Fatal("NoPropagate entry must not reach grandchildren")
	}

	// fileD: dirD's explicit erin first, then inherited root entries.
	fileD, err := snap.EffectiveACEs("fileD")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trusteeOrder(fileD), []string{"erin", "alice", "bob"}; !equal(got, want) {
		t.Fatalf("fileD ACE order = %v, want %v", got, want)
	}
}

func TestMoveRecomputesInheritanceAtomically(t *testing.T) {
	tree := buildMixedTree(t)
	snapBefore := tree.Current()

	beforeDirA, err := snapBefore.EffectiveACEs("dirA")
	if err != nil {
		t.Fatal(err)
	}
	beforeDirB, err := snapBefore.EffectiveACEs("dirB")
	if err != nil {
		t.Fatal(err)
	}

	snapAfter, err := tree.Move("dirA", "dirD")
	if err != nil {
		t.Fatalf("move dirA under dirD: %v", err)
	}
	if snapAfter.Version() != snapBefore.Version()+1 {
		t.Fatalf("generation after move = %d, want %d",
			snapAfter.Version(), snapBefore.Version()+1)
	}

	// The old snapshot stays fully consistent and unaffected by the move.
	oldDirA, err := snapBefore.EffectiveACEs("dirA")
	if err != nil {
		t.Fatal(err)
	}
	if !aceEqual(oldDirA, beforeDirA) {
		t.Fatalf("old snapshot mutated after move: %+v vs %+v", oldDirA, beforeDirA)
	}
	oldNode, ok := snapBefore.Node("dirA")
	if !ok || oldNode.ParentID() != RootID {
		t.Fatal("old snapshot must retain the pre-move parent linkage")
	}

	// The new snapshot reflects the reparenting.
	movedNode, ok := snapAfter.Node("dirA")
	if !ok || movedNode.ParentID() != "dirD" {
		t.Fatalf("post-move parent = %v, want dirD", movedNode)
	}

	// Explicit entries survive unchanged; the explicit deny stays first.
	afterDirA, err := snapAfter.EffectiveACEs("dirA")
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"mallory", "oscar", "erin", "alice", "bob"}
	if got := trusteeOrder(afterDirA); !equal(got, wantOrder) {
		t.Fatalf("post-move dirA ACE order = %v, want %v", got, wantOrder)
	}
	mallory := findACE(t, afterDirA, "mallory")
	if mallory.Type != ACEDeny || mallory.IsInherited() {
		t.Fatalf("explicit deny must survive the move: %+v", mallory)
	}
	erin := findACE(t, afterDirA, "erin")
	if !erin.IsInherited() {
		t.Fatalf("erin must be inherited from the new parent: %+v", erin)
	}
	if hasTrustee(beforeDirA, "erin") {
		t.Fatal("pre-move dirA must not contain erin")
	}

	// Descendants are recomputed through the new ancestry as well.
	afterFileB, err := snapAfter.EffectiveACEs("fileB")
	if err != nil {
		t.Fatal(err)
	}
	wantFileB := []string{"oscar", "erin", "alice", "bob"}
	if got := trusteeOrder(afterFileB); !equal(got, wantFileB) {
		t.Fatalf("post-move fileB ACE order = %v, want %v", got, wantFileB)
	}

	// The protected subtree keeps broken inheritance across the move: it
	// gains nothing from the new ancestry and keeps only explicit entries.
	afterDirB, err := snapAfter.EffectiveACEs("dirB")
	if err != nil {
		t.Fatal(err)
	}
	if !aceEqual(afterDirB, beforeDirB) {
		t.Fatalf("protected subtree changed after move: %+v vs %+v", afterDirB, beforeDirB)
	}
	afterFileE, err := snapAfter.EffectiveACEs("fileE")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trusteeOrder(afterFileE), []string{"bob", "mallory"}; !equal(got, want) {
		t.Fatalf("post-move fileE ACE order = %v, want %v", got, want)
	}
	afterDirC, err := snapAfter.EffectiveACEs("dirC")
	if err != nil {
		t.Fatal(err)
	}
	for _, ace := range afterDirC {
		// The protection boundary must hide the entire new ancestry; only
		// entries defined on or below dirB are allowed inside the subtree.
		if ace.Trustee == "erin" || ace.Trustee == "alice" || ace.Trustee == "carol" {
			t.Fatalf("protected subtree leaked ancestor entry through new ancestry: %+v", ace)
		}
	}

	// NP is recomputed relative to the new ancestry: carol was already spent
	// at dirA's old level, so it is absent from the deeper post-move dirA and
	// from fileB. The files-only (OI|IO) bob instead keeps traversing through
	// the moved container to the file below.
	if hasTrustee(afterDirA, "carol") || hasTrustee(afterFileB, "carol") {
		t.Fatal("NoPropagate entry must not survive into the re-homed subtree")
	}
	if !hasTrustee(afterFileB, "bob") {
		t.Fatal("inherit-only OI entry must keep traversing directories to files")
	}

	// fileD is now a sibling of the moved subtree and remains unchanged.
	afterFileD, err := snapAfter.EffectiveACEs("fileD")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trusteeOrder(afterFileD), []string{"erin", "alice", "bob"}; !equal(got, want) {
		t.Fatalf("fileD ACE order = %v, want %v", got, want)
	}
}

func TestMoveErrorsAndConcurrency(t *testing.T) {
	tree := buildMixedTree(t)

	cases := []struct {
		name      string
		id        string
		newParent string
		wantErr   error
	}{
		{"cannot move root", RootID, "dirD", ErrCannotMoveRoot},
		{"unknown node", "ghost", "dirD", ErrNoSuchNode},
		{"unknown parent", "dirA", "ghost", ErrNoSuchNode},
		{"parent is a file", "dirA", "fileB", ErrParentNotContainer},
		{"move into self", "dirA", "dirA", ErrMoveIntoDescendant},
		{"move into descendant", "dirA", "dirB", ErrMoveIntoDescendant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tree.Move(tc.id, tc.newParent)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Move(%q, %q) error = %v, want %v", tc.id, tc.newParent, err, tc.wantErr)
			}
		})
	}

	// Concurrent readers must always observe a complete, internally
	// consistent generation while moves publish new ones.
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := tree.Current()
				aces, err := snap.EffectiveACEs("dirB")
				if err != nil {
					t.Errorf("read effective aces: %v", err)
					return
				}
				for _, ace := range aces {
					if ace.IsInherited() {
						t.Errorf("torn read: protected dirB exposed inherited ACE %+v", ace)
						return
					}
				}
				runtime.Gosched()
			}
		}()
	}

	for range 20 {
		_, err := tree.Move("dirB", "dirD")
		if err != nil {
			t.Fatalf("move dirB: %v", err)
		}
		if _, err := tree.Move("dirB", "dirA"); err != nil {
			t.Fatalf("move dirB back: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}
