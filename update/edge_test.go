package update

import (
	"context"
	"errors"
	"testing"
)

func TestRefreshWithoutBootstrap(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	client := NewClient(NewFileStore(t.TempDir())).WithClock(env.clock())

	_, rootV1 := env.rootV1()
	bundle := env.refreshBundle(rootV1, nil, 1, 1, 1, []byte("p"))
	assertErrorIs(t, client.Refresh(ctx, bundle), ErrNoState)

	if _, err := client.TrustedRootVersion(ctx); !errors.Is(err, ErrNoState) {
		t.Fatalf("expected ErrNoState, got %v", err)
	}
}

func TestRefreshRequiresCoreRoles(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	client := env.bootstrapClient(ctx, NewFileStore(t.TempDir()))

	_, rootV1 := env.rootV1()
	bundle := env.refreshBundle(rootV1, nil, 1, 1, 1, []byte("p"))

	missingTimestamp := bundle
	missingTimestamp.Timestamp = nil
	assertErrorIs(t, client.Refresh(ctx, missingTimestamp), ErrMissingMetadata)

	missingSnapshot := bundle
	missingSnapshot.Snapshot = nil
	assertErrorIs(t, client.Refresh(ctx, missingSnapshot), ErrMissingMetadata)

	// An extra target with no snapshot reference cannot be committed.
	extra := bundle
	extra.Targets = map[string]*Envelope{
		testTargetPath: bundle.Targets[testTargetPath],
		"other/file":   bundle.Targets[testTargetPath],
	}
	assertErrorIs(t, client.Refresh(ctx, extra), ErrMissingMetadata)
}

func TestBootstrapRejectsNilRoot(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	client := NewClient(NewFileStore(t.TempDir())).WithClock(env.clock())
	assertErrorIs(t, client.Bootstrap(ctx, nil), ErrMissingMetadata)
}
