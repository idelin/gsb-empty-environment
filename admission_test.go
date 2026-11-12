package taskpkg

import (
	"errors"
	"reflect"
	"slices"
	"sort"
	"testing"
)

func ackMessage(gen Generation, dir Direction, intervals ...Interval) AckMessage {
	return AckMessage{
		Generation: gen,
		Direction:  dir,
		Intervals:  intervals,
	}
}

func interval(first, last PacketNumber) Interval {
	return Interval{First: first, Last: last}
}

func permissiveAuthenticator(calls *int) Authenticator {
	return AuthenticatorFunc(func(msg AckMessage, expected Identity) error {
		if calls != nil {
			*calls++
		}
		return nil
	})
}

func mustAdmission(t *testing.T, auth Authenticator, cfg Config) *Admission {
	t.Helper()
	admission, err := NewAdmission(auth, cfg)
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	return admission
}

func TestSparseSendFakeAckAndDuplicateIntervals(t *testing.T) {
	var authCalls int
	released := make(map[FrameID]int)
	var admission *Admission
	admission = mustAdmission(t, permissiveAuthenticator(&authCalls), Config{
		Generation:        7,
		Direction:         DirectionOutbound,
		PacketNumberLimit: 16,
		OnFrameReleased: func(frame FrameID) {
			// A callback that re-enters the admission would deadlock if the
			// callback ran under the admission lock.
			if got := admission.FrameRefs(frame); got != 0 {
				t.Errorf("FrameRefs(%d) during release callback = %d, want 0", frame, got)
			}
			released[frame]++
		},
	})

	if err := admission.Send(1, 7); err != nil {
		t.Fatalf("Send(1): %v", err)
	}
	if err := admission.Send(3, 7, 8); err != nil {
		t.Fatalf("Send(3): %v", err)
	}
	if err := admission.Send(7, 9); err != nil {
		t.Fatalf("Send(7): %v", err)
	}

	if got, want := admission.FrameRefs(7), 2; got != want {
		t.Fatalf("FrameRefs(7) = %d, want %d", got, want)
	}
	if got, want := admission.FrameRefs(8), 1; got != want {
		t.Fatalf("FrameRefs(8) = %d, want %d", got, want)
	}

	result, err := admission.AdmitAck(ackMessage(7, DirectionOutbound, interval(1, 3)))
	if !errors.Is(err, ErrUnsentPacket) {
		t.Fatalf("fake ack error = %v, want ErrUnsentPacket", err)
	}
	if result.Rejected == nil || result.Rejected.Number != 2 {
		t.Fatalf("fake ack rejected = %+v, want smallest unsent 2", result.Rejected)
	}
	if number, ok := SmallestUnsent(err); !ok || number != 2 {
		t.Fatalf("SmallestUnsent = %d, %v; want 2, true", number, ok)
	}
	if authCalls != 1 {
		t.Fatalf("authenticator calls = %d, want 1", authCalls)
	}
	if got := admission.OutstandingPackets(); got != 3 {
		t.Fatalf("outstanding after fake ack = %d, want 3", got)
	}
	if admission.FrameRefs(7) != 2 || admission.FrameRefs(8) != 1 || admission.FrameRefs(9) != 1 {
		t.Fatalf("fake ack changed frame references: 7=%d 8=%d 9=%d",
			admission.FrameRefs(7), admission.FrameRefs(8), admission.FrameRefs(9))
	}

	result, err = admission.AdmitAck(ackMessage(
		7, DirectionOutbound,
		interval(1, 1), interval(1, 1), interval(3, 3), interval(1, 1),
	))
	if err != nil {
		t.Fatalf("valid duplicate ack: %v", err)
	}
	if got, want := result.Packets, []PacketNumber{1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("acknowledged packets = %v, want %v", got, want)
	}
	if got, want := result.ReleasedFrames, []FrameID{7, 8}; !reflect.DeepEqual(got, want) {
		t.Fatalf("released frames = %v, want %v", got, want)
	}
	if got, want := released, map[FrameID]int{7: 1, 8: 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("release callbacks = %v, want %v", got, want)
	}
	if admission.FrameRefs(7) != 0 || admission.FrameRefs(8) != 0 || admission.FrameRefs(9) != 1 {
		t.Fatalf("bad references after valid ack: 7=%d 8=%d 9=%d",
			admission.FrameRefs(7), admission.FrameRefs(8), admission.FrameRefs(9))
	}

	result, err = admission.AdmitAck(ackMessage(
		7, DirectionOutbound, interval(1, 1), interval(3, 3),
	))
	if err != nil {
		t.Fatalf("repeated ack: %v", err)
	}
	if result.AcknowledgedPackets != 0 || len(result.ReleasedFrames) != 0 {
		t.Fatalf("repeated ack had an effect: %+v", result)
	}
	if admission.FrameRefs(7) != 0 || admission.FrameRefs(8) != 0 || admission.FrameRefs(9) != 1 {
		t.Fatalf("repeated ack changed references: 7=%d 8=%d 9=%d",
			admission.FrameRefs(7), admission.FrameRefs(8), admission.FrameRefs(9))
	}

	result, err = admission.AdmitAck(ackMessage(7, DirectionOutbound, interval(7, 7)))
	if err != nil {
		t.Fatalf("ack packet 7: %v", err)
	}
	if got, want := result.ReleasedFrames, []FrameID{9}; !reflect.DeepEqual(got, want) {
		t.Fatalf("released frames = %v, want %v", got, want)
	}
	if released[9] != 1 {
		t.Fatalf("frame 9 release callbacks = %d, want 1", released[9])
	}
	if got := admission.OutstandingPackets(); got != 0 {
		t.Fatalf("outstanding = %d, want 0", got)
	}
}

func TestWholeAckIsRejectedOnAnyUnsentInterval(t *testing.T) {
	admission := mustAdmission(t, permissiveAuthenticator(nil), Config{
		Generation:        3,
		Direction:         DirectionOutbound,
		PacketNumberLimit: 8,
	})

	for _, packetNumber := range []PacketNumber{0, 1, 4, 5} {
		if err := admission.Send(packetNumber, 42); err != nil {
			t.Fatalf("Send(%d): %v", packetNumber, err)
		}
	}
	if got, want := admission.FrameRefs(42), 4; got != want {
		t.Fatalf("FrameRefs(42) = %d, want %d", got, want)
	}

	tests := []struct {
		name      string
		intervals []Interval
		want      PacketNumber
	}{
		{
			name:      "gap inside one interval",
			intervals: []Interval{interval(0, 5)},
			want:      2,
		},
		{
			name:      "smaller failure in a later interval",
			intervals: []Interval{interval(4, 7), interval(2, 9)},
			want:      2,
		},
		{
			name:      "duplicate valid interval cannot mask invalid interval",
			intervals: []Interval{interval(0, 1), interval(0, 1), interval(2, 5)},
			want:      2,
		},
		{
			name:      "first failure inside a later interval",
			intervals: []Interval{interval(0, 1), interval(3, 5)},
			want:      3,
		},
		{
			name:      "outside configured space",
			intervals: []Interval{interval(0, 1), interval(8, 8)},
			want:      8,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			beforeOutstanding := admission.OutstandingPackets()
			result, err := admission.AdmitAck(ackMessage(3, DirectionOutbound, test.intervals...))
			if !errors.Is(err, ErrUnsentPacket) {
				t.Fatalf("AdmitAck error = %v, want ErrUnsentPacket", err)
			}
			if result.Admitted || result.AcknowledgedPackets != 0 {
				t.Fatalf("rejected ack had an effect: %+v", result)
			}
			if result.Rejected == nil || result.Rejected.Number != test.want {
				t.Fatalf("rejected = %+v, want smallest unsent %d", result.Rejected, test.want)
			}
			if number, ok := SmallestUnsent(err); !ok || number != test.want {
				t.Fatalf("SmallestUnsent = %d, %v; want %d, true", number, ok, test.want)
			}
			if admission.OutstandingPackets() != beforeOutstanding {
				t.Fatalf("outstanding changed from %d to %d",
					beforeOutstanding, admission.OutstandingPackets())
			}
			if got, want := admission.FrameRefs(42), 4; got != want {
				t.Fatalf("FrameRefs(42) = %d, want %d after rejected ack", got, want)
			}
		})
	}

	result, err := admission.AdmitAck(ackMessage(
		3, DirectionOutbound,
		interval(0, 1), interval(0, 1), interval(4, 5),
	))
	if err != nil {
		t.Fatalf("valid ack: %v", err)
	}
	if got, want := result.Packets, []PacketNumber{0, 1, 4, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("packets = %v, want %v", got, want)
	}
	if admission.FrameRefs(42) != 0 {
		t.Fatalf("FrameRefs(42) = %d, want 0", admission.FrameRefs(42))
	}
}

func TestMalformedIntervalAndAuthenticationOrder(t *testing.T) {
	var authCalls int
	admission := mustAdmission(t, AuthenticatorFunc(func(msg AckMessage, expected Identity) error {
		authCalls++
		if msg.AuthTag != nil && string(msg.AuthTag) != "good" {
			return errors.New("bad authentication tag")
		}
		// Mutating the copy must not alter the caller's message.
		msg.Intervals = nil
		return nil
	}), Config{
		Generation:        11,
		Direction:         DirectionOutbound,
		PacketNumberLimit: 4,
	})

	if err := admission.Send(0, 10); err != nil {
		t.Fatalf("Send(0): %v", err)
	}
	if err := admission.Send(1, 10); err != nil {
		t.Fatalf("Send(1): %v", err)
	}

	result, err := admission.AdmitAck(AckMessage{
		Generation: 11,
		Direction:  DirectionOutbound,
		Intervals:  []Interval{interval(3, 2)},
		AuthTag:    []byte("good"),
	})
	if !errors.Is(err, ErrMalformedInterval) {
		t.Fatalf("malformed error = %v, want ErrMalformedInterval", err)
	}
	if authCalls != 1 {
		t.Fatalf("authenticator calls = %d, want 1", authCalls)
	}
	if result.Admitted || admission.OutstandingPackets() != 2 || admission.FrameRefs(10) != 2 {
		t.Fatalf("malformed ack changed state: %+v outstanding=%d refs=%d",
			result, admission.OutstandingPackets(), admission.FrameRefs(10))
	}

	result, err = admission.AdmitAck(AckMessage{
		Generation: 11,
		Direction:  DirectionOutbound,
		Intervals:  []Interval{interval(0, 1)},
		AuthTag:    []byte("bad"),
	})
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("bad auth error = %v, want ErrAuthentication", err)
	}
	if authCalls != 2 {
		t.Fatalf("authenticator calls = %d, want 2", authCalls)
	}
	if admission.OutstandingPackets() != 2 || admission.FrameRefs(10) != 2 {
		t.Fatalf("failed auth changed state: outstanding=%d refs=%d",
			admission.OutstandingPackets(), admission.FrameRefs(10))
	}

	// The authenticator cleared its private copy of Intervals. The admission
	// must still use the original message and acknowledge both packets.
	result, err = admission.AdmitAck(AckMessage{
		Generation: 11,
		Direction:  DirectionOutbound,
		Intervals:  []Interval{interval(0, 1)},
		AuthTag:    []byte("good"),
	})
	if err != nil {
		t.Fatalf("valid ack after auth copy mutation: %v", err)
	}
	if got, want := result.Packets, []PacketNumber{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("packets = %v, want %v", got, want)
	}
}

func TestIdentityMismatchAndConnectionChange(t *testing.T) {
	authCalls := 0
	admission := mustAdmission(t, permissiveAuthenticator(&authCalls), Config{
		Generation:        20,
		Direction:         DirectionOutbound,
		PacketNumberLimit: 4,
	})
	if err := admission.Send(0, 1); err != nil {
		t.Fatalf("Send(0): %v", err)
	}

	result, err := admission.AdmitAck(ackMessage(19, DirectionOutbound, interval(0, 0)))
	if !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("wrong generation error = %v, want ErrGenerationMismatch", err)
	}
	if authCalls != 1 {
		t.Fatalf("auth calls = %d, want 1", authCalls)
	}
	if result.Admitted || admission.OutstandingPackets() != 1 || admission.FrameRefs(1) != 1 {
		t.Fatalf("generation mismatch changed state")
	}

	result, err = admission.AdmitAck(ackMessage(20, DirectionInbound, interval(0, 0)))
	if !errors.Is(err, ErrDirectionMismatch) {
		t.Fatalf("wrong direction error = %v, want ErrDirectionMismatch", err)
	}
	if result.Admitted || admission.OutstandingPackets() != 1 || admission.FrameRefs(1) != 1 {
		t.Fatalf("direction mismatch changed state")
	}

	// A migration during authentication must invalidate the stale auth result.
	var admissionRef *Admission
	admissionRef, err = NewAdmission(AuthenticatorFunc(func(msg AckMessage, expected Identity) error {
		if _, migrateErr := admissionRef.Migrate(21, DirectionOutbound); migrateErr != nil {
			t.Errorf("Migrate during auth: %v", migrateErr)
		}
		return nil
	}), Config{
		Generation:        20,
		Direction:         DirectionOutbound,
		PacketNumberLimit: 4,
	})
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	_, err = admissionRef.AdmitAck(ackMessage(21, DirectionOutbound, interval(0, 0)))
	if !errors.Is(err, ErrConnectionChanged) {
		t.Fatalf("connection-change error = %v, want ErrConnectionChanged", err)
	}
}

func TestExhaustionAndMigration(t *testing.T) {
	var releases int
	admission := mustAdmission(t, permissiveAuthenticator(nil), Config{
		Generation:        30,
		Direction:         DirectionOutbound,
		PacketNumberLimit: 4,
		OnFrameReleased: func(FrameID) {
			releases++
		},
	})

	for _, test := range []struct {
		packet PacketNumber
		frames []FrameID
	}{
		{packet: 0, frames: []FrameID{100}},
		{packet: 1, frames: []FrameID{100}},
		{packet: 3, frames: []FrameID{101}},
	} {
		if err := admission.Send(test.packet, test.frames...); err != nil {
			t.Fatalf("Send(%d): %v", test.packet, err)
		}
	}
	if _, err := admission.SendNext(102); !errors.Is(err, ErrPacketNumberExhausted) {
		t.Fatalf("SendNext error = %v, want ErrPacketNumberExhausted", err)
	}
	if !admission.Exhausted() {
		t.Fatal("Exhausted() = false, want true")
	}
	if admission.FrameRefs(100) != 2 || admission.FrameRefs(101) != 1 {
		t.Fatalf("refs before migration: 100=%d 101=%d",
			admission.FrameRefs(100), admission.FrameRefs(101))
	}

	migration, err := admission.Migrate(31, DirectionOutbound)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if migration.From != 30 || migration.To != 31 {
		t.Fatalf("migration generations = %d -> %d, want 30 -> 31", migration.From, migration.To)
	}
	if migration.AbandonedPackets != 3 {
		t.Fatalf("AbandonedPackets = %d, want 3", migration.AbandonedPackets)
	}
	if got, want := migration.AbandonedFrames, []FrameID{100, 101}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AbandonedFrames = %v, want %v", got, want)
	}
	if releases != 0 {
		t.Fatalf("migration invoked release callback %d times, want 0", releases)
	}
	if admission.FrameRefs(100) != 0 || admission.FrameRefs(101) != 0 {
		t.Fatalf("migration did not abandon frames: 100=%d 101=%d",
			admission.FrameRefs(100), admission.FrameRefs(101))
	}
	if admission.Exhausted() {
		t.Fatal("Exhausted() = true after migration, want false")
	}
	if _, ok := admission.LastSent(); ok {
		t.Fatal("LastSent reported a packet after migration")
	}

	result, err := admission.AdmitAck(ackMessage(30, DirectionOutbound, interval(0, 0)))
	if !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("old-generation ack error = %v, want ErrGenerationMismatch", err)
	}
	if result.Admitted || admission.OutstandingPackets() != 0 {
		t.Fatalf("old-generation ack changed state")
	}

	next, err := admission.SendNext(100)
	if err != nil {
		t.Fatalf("SendNext after migration: %v", err)
	}
	if next != 0 {
		t.Fatalf("next packet after migration = %d, want 0", next)
	}
	if admission.FrameRefs(100) != 1 {
		t.Fatalf("FrameRefs(100) after new send = %d, want 1", admission.FrameRefs(100))
	}

	result, err = admission.AdmitAck(ackMessage(31, DirectionOutbound, interval(0, 0)))
	if err != nil {
		t.Fatalf("new-generation ack: %v", err)
	}
	if got, want := result.ReleasedFrames, []FrameID{100}; !reflect.DeepEqual(got, want) {
		t.Fatalf("released = %v, want %v", got, want)
	}

	if _, err := admission.Migrate(31, DirectionOutbound); !errors.Is(err, ErrGenerationNotAdvanced) {
		t.Fatalf("same-generation migration error = %v, want ErrGenerationNotAdvanced", err)
	}
	if _, err := admission.Migrate(30, DirectionOutbound); !errors.Is(err, ErrGenerationNotAdvanced) {
		t.Fatalf("lower-generation migration error = %v, want ErrGenerationNotAdvanced", err)
	}
}

func TestPacketNumbersDoNotWrap(t *testing.T) {
	admission := mustAdmission(t, permissiveAuthenticator(nil), Config{
		Generation: 40,
		Direction:  DirectionOutbound,
	})

	if err := admission.Send(PacketNumber(^uint64(0)), 1); err != nil {
		t.Fatalf("Send(max): %v", err)
	}
	if _, err := admission.SendNext(1); !errors.Is(err, ErrPacketNumberExhausted) {
		t.Fatalf("SendNext after max error = %v, want ErrPacketNumberExhausted", err)
	}
	if err := admission.Send(0, 1); !errors.Is(err, ErrPacketNumberOutOfOrder) {
		t.Fatalf("Send(0) after max error = %v, want ErrPacketNumberOutOfOrder", err)
	}
	if err := admission.Send(PacketNumber(^uint64(0)), 1); !errors.Is(err, ErrPacketNumberReused) {
		t.Fatalf("reusing max error = %v, want ErrPacketNumberReused", err)
	}

	// Checking a huge interval must not walk all 2^64 numbers.
	result, err := admission.AdmitAck(ackMessage(40, DirectionOutbound, interval(
		0, PacketNumber(^uint64(0)),
	)))
	if !errors.Is(err, ErrUnsentPacket) {
		t.Fatalf("huge interval error = %v, want ErrUnsentPacket", err)
	}
	if result.Rejected == nil || result.Rejected.Number != 0 {
		t.Fatalf("huge interval rejected = %+v, want 0", result.Rejected)
	}
	if admission.FrameRefs(1) != 1 {
		t.Fatalf("rejected huge interval decremented references: %d", admission.FrameRefs(1))
	}
	if _, err := admission.AdmitAck(ackMessage(40, DirectionOutbound, interval(
		PacketNumber(^uint64(0)), 0,
	))); !errors.Is(err, ErrMalformedInterval) {
		t.Fatalf("wrapped interval error = %v, want ErrMalformedInterval", err)
	}

	result, err = admission.AdmitAck(ackMessage(40, DirectionOutbound, interval(
		PacketNumber(^uint64(0)), PacketNumber(^uint64(0)),
	)))
	if err != nil {
		t.Fatalf("ack max packet: %v", err)
	}
	if result.Admitted != true || admission.FrameRefs(1) != 0 {
		t.Fatalf("ack max did not release frame: %+v refs=%d", result, admission.FrameRefs(1))
	}

	// A zero-length domain still permits packet number zero.
	zeroDomain := mustAdmission(t, permissiveAuthenticator(nil), Config{
		Generation:        41,
		Direction:         DirectionOutbound,
		PacketNumberLimit: 1,
	})
	if err := zeroDomain.Send(0, 2); err != nil {
		t.Fatalf("Send(0) in one-number domain: %v", err)
	}
	if err := zeroDomain.Send(1, 2); !errors.Is(err, ErrPacketNumberOutOfRange) {
		t.Fatalf("Send(1) in one-number domain error = %v, want ErrPacketNumberOutOfRange", err)
	}
}

func TestConstructorAndSparseIntervals(t *testing.T) {
	if _, err := NewAdmission(nil, Config{
		Direction: DirectionOutbound,
	}); !errors.Is(err, ErrNoAuthenticator) {
		t.Fatalf("nil authenticator error = %v, want ErrNoAuthenticator", err)
	}
	if _, err := NewAdmission(permissiveAuthenticator(nil), Config{
		Direction: 0,
	}); !errors.Is(err, ErrInvalidDirection) {
		t.Fatalf("zero direction error = %v, want ErrInvalidDirection", err)
	}
	var nilAuth AuthenticatorFunc
	if err := nilAuth.Authenticate(AckMessage{}, Identity{}); !errors.Is(err, ErrNoAuthenticator) {
		t.Fatalf("typed-nil AuthenticatorFunc error = %v, want ErrNoAuthenticator", err)
	}

	admission := mustAdmission(t, permissiveAuthenticator(nil), Config{
		Generation:        50,
		Direction:         DirectionInbound,
		PacketNumberLimit: 10,
	})
	for _, packetNumber := range []PacketNumber{0, 1, 4, 5, 9} {
		if err := admission.Send(packetNumber, FrameID(packetNumber)); err != nil {
			t.Fatalf("Send(%d): %v", packetNumber, err)
		}
	}
	got := admission.SentIntervals()
	want := []Interval{
		interval(0, 1),
		interval(4, 5),
		interval(9, 9),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SentIntervals = %v, want %v", got, want)
	}
	for _, packetNumber := range []PacketNumber{0, 1, 4, 5, 9} {
		if !admission.IsSent(packetNumber) {
			t.Fatalf("IsSent(%d) = false, want true", packetNumber)
		}
	}
	for _, packetNumber := range []PacketNumber{2, 3, 6, 7, 8, 10} {
		if admission.IsSent(packetNumber) {
			t.Fatalf("IsSent(%d) = true, want false", packetNumber)
		}
	}

	// Overlapping valid intervals are one union for reference decrementing;
	// each packet is acknowledged only once.
	result, err := admission.AdmitAck(ackMessage(50, DirectionInbound,
		interval(0, 1), interval(1, 1), interval(4, 5), interval(5, 5), interval(9, 9),
	))
	if err != nil {
		t.Fatalf("overlapping ack: %v", err)
	}
	if got, want := result.Packets, []PacketNumber{0, 1, 4, 5, 9}; !reflect.DeepEqual(got, want) {
		t.Fatalf("packets = %v, want %v", got, want)
	}
	if result.AcknowledgedPackets != 5 {
		t.Fatalf("AcknowledgedPackets = %d, want 5", result.AcknowledgedPackets)
	}
}

func TestReferenceModelSmallDomain(t *testing.T) {
	const limit = uint64(8)
	const frames = FrameID(6)

	var authCalls int
	admission := mustAdmission(t, permissiveAuthenticator(&authCalls), Config{
		Generation:        60,
		Direction:         DirectionOutbound,
		PacketNumberLimit: limit,
	})
	model := newReferenceModel(60, DirectionOutbound, limit)

	rng := newDeterministicRand(1)
	for step := 0; step < 2000; step++ {
		switch rng.Intn(5) {
		case 0:
			frames := referenceFrames(rng)
			var packet PacketNumber
			var err error
			packet, err = admission.SendNext(frames...)
			modelPacket, modelErr := model.sendNext(frames)
			_ = modelPacket
			if errorKind(err) != errorKind(modelErr) {
				t.Fatalf("step %d SendNext: admission err=%v model err=%v", step, err, modelErr)
			}
			if err == nil && packet != model.last {
				t.Fatalf("step %d SendNext packet=%d model=%d", step, packet, model.last)
			}
			if errors.Is(err, ErrPacketNumberExhausted) {
				migration, migrateErr := admission.Migrate(model.gen+1, model.dir)
				if migrateErr != nil {
					t.Fatalf("step %d Migrate: %v", step, migrateErr)
				}
				model.migrate(model.gen+1, model.dir)
				if migration.AbandonedPackets != model.abandonedPackets {
					t.Fatalf("step %d abandoned packets=%d model=%d",
						step, migration.AbandonedPackets, model.abandonedPackets)
				}
			}
		case 1:
			packetNumber := PacketNumber(rng.Intn(int(limit) + 2))
			frames := referenceFrames(rng)
			err := admission.Send(packetNumber, frames...)
			modelErr := model.send(packetNumber, frames)
			if errorKind(err) != errorKind(modelErr) {
				t.Fatalf("step %d Send(%d): admission err=%v model err=%v",
					step, packetNumber, err, modelErr)
			}
		case 2:
			msg := randomAckMessage(rng, model.gen, model.dir, limit)
			result, err := admission.AdmitAck(msg)
			modelResult, modelErr := model.admit(msg)
			if errorKind(err) != errorKind(modelErr) {
				t.Fatalf("step %d AdmitAck(%+v): admission err=%v model err=%v",
					step, msg, err, modelErr)
			}
			if errors.Is(err, ErrUnsentPacket) {
				if result.Rejected == nil || modelResult.Rejected == nil ||
					result.Rejected.Number != modelResult.Rejected.Number {
					t.Fatalf("step %d rejected=%+v model=%+v", step, result.Rejected, modelResult.Rejected)
				}
			}
			if !slices.Equal(result.Packets, modelResult.Packets) ||
				!slices.Equal(result.ReleasedFrames, modelResult.ReleasedFrames) ||
				result.AcknowledgedPackets != modelResult.AcknowledgedPackets ||
				result.Admitted != modelResult.Admitted {
				t.Fatalf("step %d result=%+v model=%+v", step, result, modelResult)
			}
		case 3:
			next := model.gen + Generation(1+rng.Intn(3))
			migration, err := admission.Migrate(next, model.dir)
			if err != nil {
				t.Fatalf("step %d Migrate: %v", step, err)
			}
			model.migrate(next, model.dir)
			if migration.AbandonedPackets != model.abandonedPackets ||
				!slices.Equal(migration.AbandonedFrames, model.abandonedFrames) {
				t.Fatalf("step %d migration=%+v model abandoned=%d/%v",
					step, migration, model.abandonedPackets, model.abandonedFrames)
			}
		case 4:
			if rng.Intn(2) == 0 {
				_, err := admission.Migrate(model.gen, model.dir)
				if !errors.Is(err, ErrGenerationNotAdvanced) {
					t.Fatalf("step %d non-advancing migration err=%v", step, err)
				}
			} else {
				msg := randomAckMessage(rng, model.gen+generationOne(rng), model.dir, limit)
				_, err := admission.AdmitAck(msg)
				modelErr := model.identityError(msg)
				if errorKind(err) != errorKind(modelErr) {
					t.Fatalf("step %d identity ack err=%v model=%v", step, err, modelErr)
				}
			}
		}

		assertModelEqual(t, step, admission, model, FrameID(5), PacketNumber(limit-1))
	}
	if authCalls == 0 {
		t.Fatal("authenticator was never called")
	}
}

func assertModelEqual(
	t *testing.T,
	step int,
	admission *Admission,
	model *referenceModel,
	maxFrame FrameID,
	maxPacket PacketNumber,
) {
	t.Helper()
	if got := admission.Identity(); got.Generation != model.gen || got.Direction != model.dir {
		t.Fatalf("step %d identity=%+v model=%d/%s", step, got, model.gen, model.dir)
	}
	last, hasLast := admission.LastSent()
	if last != model.last || hasLast != model.hasLast {
		t.Fatalf("step %d LastSent=%d,%v model=%d,%v",
			step, last, hasLast, model.last, model.hasLast)
	}
	if got, want := admission.Exhausted(), model.exhausted(); got != want {
		t.Fatalf("step %d Exhausted=%v model=%v", step, got, want)
	}
	if got, want := admission.OutstandingPackets(), len(model.outstanding); got != want {
		t.Fatalf("step %d outstanding=%d model=%d", step, got, want)
	}
	for packetNumber := PacketNumber(0); packetNumber <= maxPacket; packetNumber++ {
		_, got := admission.PacketFrames(packetNumber)
		_, want := model.outstanding[packetNumber]
		if got != want {
			t.Fatalf("step %d packet %d outstanding=%v model=%v",
				step, packetNumber, got, want)
		}
		if got, want := admission.IsSent(packetNumber), model.sent[packetNumber]; got != want {
			t.Fatalf("step %d IsSent(%d)=%v model=%v", step, packetNumber, got, want)
		}
	}
	for frame := FrameID(0); frame <= maxFrame; frame++ {
		if got, want := admission.FrameRefs(frame), model.refs[frame]; got != want {
			t.Fatalf("step %d FrameRefs(%d)=%d model=%d", step, frame, got, want)
		}
	}
	if got, want := admission.PendingFrames(), model.pendingFrames(); !slices.Equal(got, want) {
		t.Fatalf("step %d PendingFrames=%v model=%v", step, got, want)
	}
}

type referenceModel struct {
	gen     Generation
	dir     Direction
	limit   uint64
	sent    map[PacketNumber]bool
	last    PacketNumber
	hasLast bool

	outstanding map[PacketNumber][]FrameID
	refs        map[FrameID]int

	abandonedPackets int
	abandonedFrames  []FrameID
}

func newReferenceModel(gen Generation, dir Direction, limit uint64) *referenceModel {
	return &referenceModel{
		gen:         gen,
		dir:         dir,
		limit:       limit,
		sent:        make(map[PacketNumber]bool),
		outstanding: make(map[PacketNumber][]FrameID),
		refs:        make(map[FrameID]int),
	}
}

func (m *referenceModel) maxNumber() PacketNumber {
	if m.limit == 0 {
		return PacketNumber(^uint64(0))
	}
	return PacketNumber(m.limit - 1)
}

func (m *referenceModel) exhausted() bool {
	return m.hasLast && m.last >= m.maxNumber()
}

func (m *referenceModel) send(packetNumber PacketNumber, frames []FrameID) error {
	if packetNumber > m.maxNumber() {
		return ErrPacketNumberOutOfRange
	}
	if m.hasLast {
		if packetNumber == m.last || m.sent[packetNumber] {
			return ErrPacketNumberReused
		}
		if packetNumber < m.last {
			return ErrPacketNumberOutOfOrder
		}
	}
	unique := uniqueFrames(frames)
	m.sent[packetNumber] = true
	m.last = packetNumber
	m.hasLast = true
	m.outstanding[packetNumber] = unique
	for _, frame := range unique {
		m.refs[frame]++
	}
	return nil
}

func (m *referenceModel) sendNext(frames []FrameID) (PacketNumber, error) {
	if m.exhausted() {
		return 0, ErrPacketNumberExhausted
	}
	var next PacketNumber
	if m.hasLast {
		next = m.last + 1
	}
	return next, m.send(next, frames)
}

func (m *referenceModel) admit(msg AckMessage) (AckResult, error) {
	if err := m.identityError(msg); err != nil {
		return AckResult{}, err
	}
	for i, current := range msg.Intervals {
		if !current.Valid() {
			return AckResult{}, &MalformedIntervalError{Index: i, Interval: current}
		}
	}

	var rejected *UnsentPacketError
	for i, current := range msg.Intervals {
		for packetNumber := current.First; ; packetNumber++ {
			if !m.sent[packetNumber] {
				if rejected == nil || packetNumber < rejected.Number {
					rejected = &UnsentPacketError{
						Index:    i,
						Interval: current,
						Number:   packetNumber,
					}
				}
				break
			}
			if packetNumber == current.Last {
				break
			}
		}
	}
	if rejected != nil {
		return AckResult{Rejected: rejected}, rejected
	}

	var acknowledged []PacketNumber
	for packetNumber := range m.outstanding {
		for _, current := range msg.Intervals {
			if current.First <= packetNumber && packetNumber <= current.Last {
				acknowledged = append(acknowledged, packetNumber)
				break
			}
		}
	}
	sort.Slice(acknowledged, func(i, j int) bool { return acknowledged[i] < acknowledged[j] })

	var released []FrameID
	for _, packetNumber := range acknowledged {
		frames := m.outstanding[packetNumber]
		delete(m.outstanding, packetNumber)
		for _, frame := range frames {
			m.refs[frame]--
			if m.refs[frame] == 0 {
				delete(m.refs, frame)
				released = append(released, frame)
			}
		}
	}
	sort.Slice(released, func(i, j int) bool { return released[i] < released[j] })

	return AckResult{
		Admitted:            true,
		AcknowledgedPackets: len(acknowledged),
		Packets:             acknowledged,
		ReleasedFrames:      released,
	}, nil
}

func (m *referenceModel) identityError(msg AckMessage) error {
	if msg.Generation != m.gen {
		return ErrGenerationMismatch
	}
	if msg.Direction != m.dir {
		return ErrDirectionMismatch
	}
	return nil
}

func (m *referenceModel) migrate(next Generation, dir Direction) {
	m.abandonedPackets = len(m.outstanding)
	m.abandonedFrames = m.pendingFrames()
	m.gen = next
	m.dir = dir
	m.sent = make(map[PacketNumber]bool)
	m.last = 0
	m.hasLast = false
	m.outstanding = make(map[PacketNumber][]FrameID)
	m.refs = make(map[FrameID]int)
}

func (m *referenceModel) pendingFrames() []FrameID {
	var out []FrameID
	for frame, refs := range m.refs {
		if refs > 0 {
			out = append(out, frame)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func uniqueFrames(frames []FrameID) []FrameID {
	seen := make(map[FrameID]struct{}, len(frames))
	out := make([]FrameID, 0, len(frames))
	for _, frame := range frames {
		if _, ok := seen[frame]; ok {
			continue
		}
		seen[frame] = struct{}{}
		out = append(out, frame)
	}
	return out
}

func errorKind(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrPacketNumberOutOfRange):
		return ErrPacketNumberOutOfRange
	case errors.Is(err, ErrPacketNumberReused):
		return ErrPacketNumberReused
	case errors.Is(err, ErrPacketNumberOutOfOrder):
		return ErrPacketNumberOutOfOrder
	case errors.Is(err, ErrPacketNumberExhausted):
		return ErrPacketNumberExhausted
	case errors.Is(err, ErrGenerationMismatch):
		return ErrGenerationMismatch
	case errors.Is(err, ErrDirectionMismatch):
		return ErrDirectionMismatch
	case errors.Is(err, ErrMalformedInterval):
		return ErrMalformedInterval
	case errors.Is(err, ErrUnsentPacket):
		return ErrUnsentPacket
	default:
		return err
	}
}

type deterministicRand struct {
	state uint64
}

func newDeterministicRand(seed uint64) *deterministicRand {
	return &deterministicRand{state: seed}
}

func (r *deterministicRand) next() uint64 {
	r.state ^= r.state << 13
	r.state ^= r.state >> 7
	r.state ^= r.state << 17
	return r.state
}

func (r *deterministicRand) Intn(n int) int {
	if n <= 0 {
		panic("Intn with non-positive n")
	}
	return int(r.next() % uint64(n))
}

func referenceFrames(rng *deterministicRand) []FrameID {
	return referenceFramesFrom(rng)
}

func referenceFramesFrom(rng *deterministicRand) []FrameID {
	count := 1 + rng.Intn(3)
	frames := make([]FrameID, 0, count)
	for i := 0; i < count; i++ {
		frames = append(frames, FrameID(rng.Intn(6)))
	}
	return frames
}

func randomAckMessage(rng *deterministicRand, gen Generation, dir Direction, limit uint64) AckMessage {
	count := rng.Intn(4)
	intervals := make([]Interval, 0, count)
	for i := 0; i < count; i++ {
		first := PacketNumber(rng.Intn(int(limit) + 2))
		length := rng.Intn(4)
		last := first + PacketNumber(length)
		if rng.Intn(9) == 0 && first > 0 {
			last = first - 1
		}
		intervals = append(intervals, Interval{First: first, Last: last})
	}
	return AckMessage{Generation: gen, Direction: dir, Intervals: intervals}
}

func generationOne(rng *deterministicRand) Generation {
	return Generation(1 + rng.Intn(2))
}
