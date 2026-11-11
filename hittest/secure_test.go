package hittest

import (
	"errors"
	"testing"
)

func TestSecureClickFullAuthorizedGesture(t *testing.T) {
	scene := buildFixture()
	click := NewSecureClick(scene)

	press := click.Press(Point{X: 140, Y: 70})
	if press.Status != ClickArmed || press.Hit.Node.ID != "B" {
		t.Fatalf("press = %+v, want armed on B", press)
	}
	if !press.Hit.Node.Secure {
		t.Fatal("B must be flagged secure")
	}
	if !click.Armed() {
		t.Fatal("gesture must be armed after press")
	}

	release, err := click.Release(Point{X: 142, Y: 72})
	if err != nil {
		t.Fatalf("release error: %v", err)
	}
	if release.Status != ClickAuthorized || release.Hit.Node.ID != "B" {
		t.Fatalf("release = %+v, want authorized on B", release)
	}
	if click.Armed() {
		t.Fatal("gesture must be consumed after release")
	}
}

func TestSecureClickRejectsNonSecureSurface(t *testing.T) {
	click := NewSecureClick(buildFixture())

	press := click.Press(Point{X: 110, Y: 60}) // lands on A, not flagged secure
	if press.Status != ClickCanceledInsecure || press.Hit.Node.ID != "A" {
		t.Fatalf("press = %+v, want insecure cancel on A", press)
	}
	if click.Armed() {
		t.Fatal("insecure press must not arm")
	}
	if _, err := click.Release(Point{X: 110, Y: 60}); !errors.Is(err, ErrNoGesture) {
		t.Fatalf("release err = %v, want ErrNoGesture", err)
	}
}

func TestSecureClickRejectsMiss(t *testing.T) {
	click := NewSecureClick(buildFixture())
	press := click.Press(Point{X: 400, Y: 400})
	if press.Status != ClickCanceledNoHit {
		t.Fatalf("press = %+v, want no-hit cancel", press)
	}
}

func TestSecureClickCancelsWhenReleaseMovesToOtherReceiver(t *testing.T) {
	scene := buildFixture()
	click := NewSecureClick(scene)

	if got := click.Press(Point{X: 140, Y: 70}).Status; got != ClickArmed {
		t.Fatalf("press status = %s, want armed", got)
	}

	// Release slides into A's vertical bar: same snapshot, different node.
	release, err := click.Release(Point{X: 110, Y: 60})
	if err != nil {
		t.Fatalf("release error: %v", err)
	}
	if release.Status != ClickCanceledMoved || release.Hit.Node.ID != "A" {
		t.Fatalf("release = %+v, want moved cancel on A", release)
	}
}

func TestSecureClickCancelsWhenReleaseLeavesAllSurfaces(t *testing.T) {
	click := NewSecureClick(buildFixture())
	click.Press(Point{X: 140, Y: 70})

	release, err := click.Release(Point{X: 400, Y: 400})
	if err != nil {
		t.Fatalf("release error: %v", err)
	}
	if release.Status != ClickCanceledMoved {
		t.Fatalf("release = %+v, want moved cancel", release)
	}
}

func TestSecureClickCancelsOnHierarchyChangeWhileHeld(t *testing.T) {
	scene := buildFixture()
	click := NewSecureClick(scene)

	click.Press(Point{X: 140, Y: 70})

	// Raise the transparent overlay while the button is held. Even though
	// T does not cover this point, the hierarchy itself changed and the
	// authorization must fail closed.
	if !scene.Raise("root", "T") {
		t.Fatal("Raise failed")
	}

	release, err := click.Release(Point{X: 140, Y: 70})
	if err != nil {
		t.Fatalf("release error: %v", err)
	}
	if release.Status != ClickCanceledStale {
		t.Fatalf("release = %+v, want stale cancel", release)
	}
}

func TestSecureClickCancelsOnGeometryChangeWhileHeld(t *testing.T) {
	scene := buildFixture()
	click := NewSecureClick(scene)

	click.Press(Point{X: 140, Y: 70})

	// Shrink the button from under the held press: the captured snapshot
	// still contains B, but the scene version proves the world moved on.
	if !scene.SetShape("B", NewRegion(R(0, 0, 1, 1))) {
		t.Fatal("SetShape failed")
	}

	release, err := click.Release(Point{X: 140, Y: 70})
	if err != nil {
		t.Fatalf("release error: %v", err)
	}
	if release.Status != ClickCanceledStale {
		t.Fatalf("release = %+v, want stale cancel", release)
	}
}

func TestSecureClickOverlayInsertionCannotHijackRelease(t *testing.T) {
	scene := buildFixture()
	click := NewSecureClick(scene)

	click.Press(Point{X: 140, Y: 70})

	// Attacker inserts an intercepting overlay over the button mid-gesture.
	attack := &Node{
		ID:        "attack",
		Transform: Identity(),
		Shape:     NewRegion(R(140, 60, 170, 90)),
	}
	if !scene.AddChild("root", attack) {
		t.Fatal("AddChild failed")
	}

	release, err := click.Release(Point{X: 140, Y: 70})
	if err != nil {
		t.Fatalf("release error: %v", err)
	}
	if release.Status != ClickCanceledStale {
		t.Fatalf("release = %+v, want stale cancel, not a hijacked hit", release)
	}

	// A subsequent, fresh gesture against the new scene arms normally.
	fresh := click.Press(Point{X: 140, Y: 70})
	if fresh.Status != ClickCanceledInsecure || fresh.Hit.Node.ID != "attack" {
		t.Fatalf("fresh press = %+v, want insecure cancel on attack overlay", fresh)
	}
}

func TestSecureClickExplicitCancel(t *testing.T) {
	click := NewSecureClick(buildFixture())
	click.Press(Point{X: 140, Y: 70})
	click.Cancel()

	if click.Armed() {
		t.Fatal("cancel must disarm the gesture")
	}
	if click.Snapshot() != nil {
		t.Fatal("cancel must drop the captured snapshot")
	}
	if _, err := click.Release(Point{X: 140, Y: 70}); !errors.Is(err, ErrNoGesture) {
		t.Fatalf("release after cancel err = %v, want ErrNoGesture", err)
	}
}

func TestSecureClickScaledBoundaryRoundTrip(t *testing.T) {
	scene := buildFixture()
	click := NewSecureClick(scene)

	// B's screen clip ends at x=148 (local 12 * scale 1.5). (147,74) is
	// inside; (149,74) is beyond the clip and falls through to C's vertical
	// bar, exercising the scaled boundary in the full press/release path.
	if got := click.Press(Point{X: 147, Y: 74}).Status; got != ClickArmed {
		t.Fatalf("press inside scaled boundary = %s, want armed", got)
	}
	release, err := click.Release(Point{X: 147, Y: 74})
	if err != nil || release.Status != ClickAuthorized {
		t.Fatalf("same-point release = %+v, %v", release, err)
	}

	got := click.Press(Point{X: 149, Y: 74})
	if got.Status != ClickCanceledInsecure || got.Hit.Node.ID != "C" {
		t.Fatalf("press beyond scaled boundary = %+v, want insecure cancel on C", got)
	}
}
