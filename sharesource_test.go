package netsec

import (
	"errors"
	"reflect"
	"testing"
)

func windowSource(windowID string) Source {
	return Source{MeetingID: "meeting-1", SubjectID: "presenter-1", Kind: SourceWindow, WindowID: windowID}
}

func desktopSource() Source {
	return Source{MeetingID: "meeting-1", SubjectID: "presenter-1", Kind: SourceDesktop}
}

func bindWindow(t *testing.T, m *Manager, windowID string) (*Session, Token) {
	t.Helper()
	sess, tok, err := m.Bind(windowSource(windowID))
	if err != nil {
		t.Fatalf("Bind window: %v", err)
	}
	return sess, tok
}

// 合法窗口共享: bound window capture works and only the declared window is
// recorded; recording scope and participant list stay in sync.
func TestLegitimateWindowShare(t *testing.T) {
	m := NewManager(nil)
	sess, tok := bindWindow(t, m, "win-42")

	if err := m.SyncParticipants(sess.ID, []string{"alice", "bob"}); err != nil {
		t.Fatalf("SyncParticipants: %v", err)
	}
	frame := Frame{Source: windowSource("win-42"), Data: []byte("frame")}
	if err := m.Capture(tok.Value, frame); err != nil {
		t.Fatalf("Capture declared window: %v", err)
	}

	rec := sess.Recording()
	if rec.Scope != "window:win-42" {
		t.Fatalf("recording scope = %q, want window:win-42", rec.Scope)
	}
	if !reflect.DeepEqual(rec.Participants, []string{"alice", "bob"}) {
		t.Fatalf("recording participants = %v", rec.Participants)
	}
	if len(rec.Frames) != 1 || rec.Frames[0].Scope != "window:win-42" {
		t.Fatalf("recorded frames = %+v", rec.Frames)
	}
	if !reflect.DeepEqual(rec.Frames[0].Participants, []string{"alice", "bob"}) {
		t.Fatalf("frame participants = %v", rec.Frames[0].Participants)
	}

	// A frame from any other window is outside the declared scope.
	other := Frame{Source: windowSource("win-99"), Data: []byte("x")}
	if err := m.Capture(tok.Value, other); !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("Capture other window err = %v, want ErrScopeMismatch", err)
	}
	if got := len(sess.Recording().Frames); got != 1 {
		t.Fatalf("out-of-scope frame recorded, frames = %d", got)
	}
}

// 源替换: the attacker swaps the capture source after binding; frames
// claiming a different window or the desktop are rejected even with the
// valid token.
func TestSourceReplacementRejected(t *testing.T) {
	m := NewManager(DesktopPrompterFunc(func(_, _ string) bool { return true }))
	sess, tok := bindWindow(t, m, "win-42")

	replacedWindow := Frame{Source: windowSource("win-evil"), Data: []byte("x")}
	if err := m.Capture(tok.Value, replacedWindow); !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("replaced window err = %v, want ErrScopeMismatch", err)
	}
	replacedDesktop := Frame{Source: desktopSource(), Data: []byte("x")}
	if err := m.Capture(tok.Value, replacedDesktop); !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("replaced desktop err = %v, want ErrScopeMismatch", err)
	}
	// A desktop claim smuggled under the bound window id is still desktop
	// scope and must not validate as the declared window.
	if got := len(sess.Recording().Frames); got != 0 {
		t.Fatalf("replaced source recorded, frames = %d", got)
	}
}

// 令牌复用: after a confirmed switch the old token is revoked and cannot be
// reused; unknown tokens are rejected outright.
func TestTokenReuseRejected(t *testing.T) {
	m := NewManager(nil)
	sess, oldTok := bindWindow(t, m, "win-42")

	req, err := m.RequestSwitch(sess.ID, windowSource("win-43"))
	if err != nil {
		t.Fatalf("RequestSwitch: %v", err)
	}
	if _, _, err := m.ConfirmSwitch(sess.ID, req.ID); err != nil {
		t.Fatalf("ConfirmSwitch: %v", err)
	}

	reuse := Frame{Source: windowSource("win-42"), Data: []byte("x")}
	if err := m.Capture(oldTok.Value, reuse); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("reused token err = %v, want ErrTokenRevoked", err)
	}
	if err := m.Capture("forged-token", reuse); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("forged token err = %v, want ErrTokenInvalid", err)
	}
	if got := len(sess.Recording().Frames); got != 0 {
		t.Fatalf("revoked token recorded, frames = %d", got)
	}
}

// 桌面提升: escalating a window share to desktop requires the explicit
// desktop prompt, both on initial bind and on switch.
func TestDesktopEscalationRequiresPrompt(t *testing.T) {
	allow := false
	prompts := 0
	m := NewManager(DesktopPrompterFunc(func(meeting, subject string) bool {
		prompts++
		if meeting != "meeting-1" || subject != "presenter-1" {
			t.Errorf("prompt identity = %s/%s", meeting, subject)
		}
		return allow
	}))

	// Initial desktop bind is declined without consent.
	if _, _, err := m.Bind(desktopSource()); !errors.Is(err, ErrDesktopDeclined) {
		t.Fatalf("desktop bind err = %v, want ErrDesktopDeclined", err)
	}

	sess, tok := bindWindow(t, m, "win-42")
	req, err := m.RequestSwitch(sess.ID, desktopSource())
	if err != nil {
		t.Fatalf("RequestSwitch desktop: %v", err)
	}
	// Prompt declined: switch rejected, window source and token stay active.
	if _, _, err := m.ConfirmSwitch(sess.ID, req.ID); !errors.Is(err, ErrDesktopDeclined) {
		t.Fatalf("desktop switch err = %v, want ErrDesktopDeclined", err)
	}
	if err := m.Capture(tok.Value, Frame{Source: windowSource("win-42")}); err != nil {
		t.Fatalf("window capture after declined switch: %v", err)
	}

	// Explicit consent: switch succeeds and scope becomes the desktop.
	allow = true
	if _, _, err := m.ConfirmSwitch(sess.ID, req.ID); err != nil {
		t.Fatalf("consented desktop switch: %v", err)
	}
	if rec := sess.Recording(); rec.Scope != "desktop:*" {
		t.Fatalf("recording scope = %q, want desktop:*", rec.Scope)
	}
	if prompts < 3 {
		t.Fatalf("desktop prompt count = %d, want >= 3", prompts)
	}
}

// 切换验证: a requested switch changes nothing until confirmed; confirming
// with the wrong request id fails; a confirmed switch rotates the token,
// updates the recording scope and keeps the synced participants.
func TestSwitchRequiresConfirmation(t *testing.T) {
	m := NewManager(nil)
	sess, tok := bindWindow(t, m, "win-42")
	if err := m.SyncParticipants(sess.ID, []string{"alice"}); err != nil {
		t.Fatalf("SyncParticipants: %v", err)
	}

	req, err := m.RequestSwitch(sess.ID, windowSource("win-43"))
	if err != nil {
		t.Fatalf("RequestSwitch: %v", err)
	}

	// Pending switch: old source and token still authoritative.
	if sess.Source.WindowID != "win-42" {
		t.Fatalf("source changed before confirmation: %s", sess.Source.WindowID)
	}
	if err := m.Capture(tok.Value, Frame{Source: windowSource("win-42")}); err != nil {
		t.Fatalf("capture with pending switch: %v", err)
	}
	if err := m.Capture(tok.Value, Frame{Source: windowSource("win-43")}); !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("unconfirmed target capture err = %v, want ErrScopeMismatch", err)
	}
	if _, _, err := m.ConfirmSwitch(sess.ID, "wrong-request"); !errors.Is(err, ErrSwitchNotPending) {
		t.Fatalf("wrong request id err = %v, want ErrSwitchNotPending", err)
	}

	// A switch cannot move the share to another meeting or subject.
	cross := windowSource("win-44")
	cross.SubjectID = "attacker"
	if _, err := m.RequestSwitch(sess.ID, cross); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("cross-subject switch err = %v, want ErrIdentityMismatch", err)
	}

	_, newTok, err := m.ConfirmSwitch(sess.ID, req.ID)
	if err != nil {
		t.Fatalf("ConfirmSwitch: %v", err)
	}
	if newTok.Value == tok.Value {
		t.Fatal("token not rotated on switch")
	}
	if err := m.Capture(newTok.Value, Frame{Source: windowSource("win-43")}); err != nil {
		t.Fatalf("capture after confirmed switch: %v", err)
	}
	rec := sess.Recording()
	if rec.Scope != "window:win-43" {
		t.Fatalf("recording scope = %q, want window:win-43", rec.Scope)
	}
	if !reflect.DeepEqual(rec.Participants, []string{"alice"}) {
		t.Fatalf("participants lost on switch: %v", rec.Participants)
	}
	if sess.PendingSwitch() != nil {
		t.Fatal("pending switch not cleared after confirmation")
	}
}
