package revocation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeStream struct {
	inbox  chan Message
	failCh chan error
	reqCh  chan Checkpoint
}

func newFakeStream() *fakeStream {
	return &fakeStream{
		inbox:  make(chan Message, 16),
		failCh: make(chan error, 1),
		reqCh:  make(chan Checkpoint, 16),
	}
}

func (f *fakeStream) Recv(ctx context.Context) (Message, error) {
	select {
	case msg := <-f.inbox:
		return msg, nil
	case err := <-f.failCh:
		return Message{}, err
	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}

func (f *fakeStream) RequestSnapshot(ctx context.Context, at Checkpoint) error {
	select {
	case f.reqCh <- at:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeStream) Close() error { return nil }

type fakeClient struct {
	mu       sync.Mutex
	froms    []Checkpoint
	streams  []*fakeStream
	failures int
}

func (c *fakeClient) Subscribe(ctx context.Context, from Checkpoint) (Stream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.froms = append(c.froms, from)
	if c.failures > 0 {
		c.failures--
		return nil, errors.New("dial failed")
	}
	stream := newFakeStream()
	c.streams = append(c.streams, stream)
	return stream, nil
}

func (c *fakeClient) lastFrom() Checkpoint {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.froms[len(c.froms)-1]
}

func (c *fakeClient) streamAt(i int) *fakeStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.streams[i]
}

func (c *fakeClient) streamCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.streams)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func testBackoff() RunOption {
	return WithBackoff(time.Millisecond, 10*time.Millisecond)
}

func TestRunDeliversEventsAndStopsOnCancel(t *testing.T) {
	sub := newTestSubscriber(t)
	client := &fakeClient{}

	ctx, cancel := context.WithCancel(context.Background())
	subscription := Run(ctx, sub, client, testBackoff())

	waitFor(t, "first stream", func() bool {
		return client.streamCount() > 0
	})
	stream := client.streamAt(0)
	stream.inbox <- Message{Kind: MessageEvent, Event: revoke(1, 1, "alice")}
	stream.inbox <- Message{Kind: MessageCheckpoint, Checkpoint: Checkpoint{Generation: 1, Sequence: 1}}

	waitFor(t, "subscriber to sync", sub.Synced)
	if !sub.IsRevoked("alice") {
		t.Fatal("streamed revocation must be visible")
	}

	cancel()
	if err := subscription.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait() = %v, want context.Canceled", err)
	}
}

func TestRunReconnectsAndResumesFromAppliedPosition(t *testing.T) {
	sub := newTestSubscriber(t)
	client := &fakeClient{failures: 1}

	ctx, cancel := context.WithCancel(context.Background())
	subscription := Run(ctx, sub, client, testBackoff())
	defer func() {
		cancel()
		_ = subscription.Wait()
	}()

	waitFor(t, "reconnect after dial failure", func() bool {
		return client.streamCount() > 0
	})

	first := client.streamAt(0)
	first.inbox <- Message{Kind: MessageEvent, Event: revoke(1, 1, "alice")}
	first.inbox <- Message{Kind: MessageCheckpoint, Checkpoint: Checkpoint{Generation: 1, Sequence: 1}}
	waitFor(t, "subscriber to sync", sub.Synced)

	first.failCh <- errors.New("connection reset")
	waitFor(t, "second stream", func() bool {
		return client.streamCount() > 1
	})
	if got, want := client.lastFrom(), (Checkpoint{Generation: 1, Sequence: 1}); got != want {
		t.Fatalf("resume position = %+v, want %+v", got, want)
	}

	second := client.streamAt(1)
	second.inbox <- Message{Kind: MessageEvent, Event: revoke(1, 2, "bob")}
	second.inbox <- Message{Kind: MessageCheckpoint, Checkpoint: Checkpoint{Generation: 1, Sequence: 2}}
	waitFor(t, "subscriber to resync", func() bool {
		return sub.Synced() && sub.IsRevoked("bob")
	})
}

func TestRunPumpsSnapshotRequestsToStream(t *testing.T) {
	sub := newTestSubscriber(t)
	client := &fakeClient{}

	ctx, cancel := context.WithCancel(context.Background())
	subscription := Run(ctx, sub, client, testBackoff())
	defer func() {
		cancel()
		_ = subscription.Wait()
	}()

	waitFor(t, "first stream", func() bool {
		return client.streamCount() > 0
	})
	stream := client.streamAt(0)

	stream.inbox <- Message{Kind: MessageEvent, Event: revoke(1, 1, "alice")}
	stream.inbox <- Message{Kind: MessageCheckpoint, Checkpoint: Checkpoint{Generation: 1, Sequence: 1}}
	waitFor(t, "subscriber to sync", sub.Synced)

	stream.inbox <- Message{Kind: MessageEvent, Event: revoke(1, 3, "carol")}

	select {
	case at := <-stream.reqCh:
		if want := (Checkpoint{Generation: 1, Sequence: 1}); at != want {
			t.Fatalf("snapshot request = %+v, want %+v", at, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream never received the snapshot request")
	}
	if sub.Valid("alice") {
		t.Fatal("subscriber must deny while the repair is in flight")
	}

	stream.inbox <- Message{Kind: MessageSnapshot, Snapshot: Snapshot{
		Generation:   1,
		LastSequence: 2,
		Revoked:      map[string]bool{"alice": true},
	}}
	stream.inbox <- Message{Kind: MessageCheckpoint, Checkpoint: Checkpoint{Generation: 1, Sequence: 3}}
	waitFor(t, "subscriber to repair", func() bool {
		return sub.Synced()
	})
	if !sub.IsRevoked("carol") {
		t.Fatal("buffered event must merge on top of the snapshot")
	}
}
