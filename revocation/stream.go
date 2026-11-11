package revocation

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// MessageKind identifies the payload carried by a [Message].
type MessageKind int

const (
	// MessageEvent carries an ordered log event.
	MessageEvent MessageKind = iota
	// MessageCheckpoint carries an authoritative high-water mark.
	MessageCheckpoint
	// MessageSnapshot carries a full repair snapshot.
	MessageSnapshot
)

// Message is one frame of the revocation stream.
type Message struct {
	Kind       MessageKind
	Event      Event
	Checkpoint Checkpoint
	Snapshot   Snapshot
}

// Stream is an open subscription to the revocation service.
type Stream interface {
	// Recv blocks until the next message arrives, the stream ends, or ctx is
	// cancelled. Implementations must return an error on cancellation.
	Recv(ctx context.Context) (Message, error)
	// RequestSnapshot asks the publisher for a full snapshot at or after the
	// given coordinate.
	RequestSnapshot(ctx context.Context, at Checkpoint) error
	// Close releases stream resources.
	Close() error
}

// Client dials the revocation service. Implementations are typically thin
// wrappers over gRPC or WebSocket streams.
type Client interface {
	Subscribe(ctx context.Context, from Checkpoint) (Stream, error)
}

// RunOption tunes a [Subscription].
type RunOption func(*runConfig)

type runConfig struct {
	backoffInitial time.Duration
	backoffMax     time.Duration
}

// WithBackoff overrides the reconnect backoff bounds. The initial delay
// doubles after each failed attempt, capped at max.
func WithBackoff(initial, max time.Duration) RunOption {
	return func(cfg *runConfig) {
		if initial > 0 {
			cfg.backoffInitial = initial
		}
		if max > 0 {
			cfg.backoffMax = max
		}
	}
}

// Subscription is a running revocation consumer.
type Subscription struct {
	done chan struct{}
	err  error
}

// Done closes when the subscription stops.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Wait blocks until the subscription stops and returns its terminal error.
// A subscription stopped by context cancellation reports the context error.
func (s *Subscription) Wait() error {
	<-s.done
	return s.err
}

// Run starts consuming the revocation stream in the background. The
// subscription resumes from the subscriber's last applied position after
// every reconnect and pumps outstanding snapshot requests to the publisher.
// It stops only when ctx is cancelled.
func Run(ctx context.Context, sub *Subscriber, client Client, opts ...RunOption) *Subscription {
	cfg := runConfig{
		backoffInitial: 50 * time.Millisecond,
		backoffMax:     5 * time.Second,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	subscription := &Subscription{done: make(chan struct{})}
	go func() {
		defer close(subscription.done)
		subscription.err = runLoop(ctx, sub, client, cfg)
	}()
	return subscription
}

func runLoop(ctx context.Context, sub *Subscriber, client Client, cfg runConfig) error {
	delay := cfg.backoffInitial
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		stream, err := client.Subscribe(ctx, sub.Position())
		if err == nil {
			delay = cfg.backoffInitial
			err = serveStream(ctx, sub, stream)
			_ = stream.Close()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if waitErr := sleep(ctx, delay); waitErr != nil {
				return waitErr
			}
			delay = min(delay*2, cfg.backoffMax)
		}
	}
}

func serveStream(ctx context.Context, sub *Subscriber, stream Stream) error {
	// Derive a per-stream context so the receive pump always terminates when
	// this stream is torn down, even if Recv is slow to honor cancellation.
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	recv := make(chan recvResult, 1)
	go func() {
		for {
			msg, err := stream.Recv(streamCtx)
			result := recvResult{msg: msg, err: err}
			select {
			case recv <- result:
				if err != nil {
					return
				}
			case <-streamCtx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case at := <-sub.SnapshotRequests():
			if err := stream.RequestSnapshot(ctx, at); err != nil {
				return fmt.Errorf("revocation: request snapshot: %w", err)
			}
		case result := <-recv:
			if result.err != nil {
				return result.err
			}
			if err := applyMessage(sub, result.msg); err != nil {
				return err
			}
		}
	}
}

type recvResult struct {
	msg Message
	err error
}

func applyMessage(sub *Subscriber, msg Message) error {
	switch msg.Kind {
	case MessageEvent:
		return sub.ApplyEvent(msg.Event)
	case MessageCheckpoint:
		return sub.ApplyCheckpoint(msg.Checkpoint)
	case MessageSnapshot:
		err := sub.ApplySnapshot(msg.Snapshot)
		// A snapshot may be delivered twice across reconnects; duplicates are
		// not fatal to the stream.
		if errors.Is(err, ErrUnexpectedSnapshot) {
			return nil
		}
		return err
	default:
		return fmt.Errorf("revocation: unknown message kind %d", msg.Kind)
	}
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
