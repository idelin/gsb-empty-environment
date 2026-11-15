package netsec

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func testStart() time.Time {
	return time.Unix(1_700_000_000, 0).UTC()
}

func testKey(t *testing.T, fill byte) *Key {
	t.Helper()
	material := bytes.Repeat([]byte{fill}, 32)
	// Keep tests repeatable under -count while still exercising the
	// process-wide round-reuse guard.
	binary.BigEndian.PutUint32(material[:4], testKeySequence.Add(1))
	key, err := NewKey(material)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return key
}

var testKeySequence atomic.Uint32

func testEncoder(t *testing.T, plan Plan, round uint32, key *Key, clock Clock) (*Encoder, Schedule) {
	t.Helper()
	schedule := Schedule{Start: testStart(), Interval: time.Second}
	enc, err := NewEncoder(Config{
		Plan:     plan,
		Schedule: schedule,
		Round:    round,
		Key:      key,
		Clock:    clock,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	return enc, schedule
}

func emitAll(t *testing.T, enc *Encoder, clock *fakeClock, schedule Schedule, before func(slot uint64)) []Frame {
	t.Helper()
	frames := make([]Frame, 0, int(enc.plan.Slots))
	for slot := uint64(0); slot < enc.plan.Slots; slot++ {
		clock.Set(schedule.SlotTime(slot))
		if before != nil {
			before(slot)
		}
		frame, err := enc.EmitNext()
		if err != nil {
			t.Fatalf("EmitNext(slot %d): %v", slot, err)
		}
		frames = append(frames, frame)
	}
	return frames
}

type trajectoryPoint struct {
	slot   uint64
	length int
	sendAt time.Time
}

func trajectory(frames []Frame) []trajectoryPoint {
	points := make([]trajectoryPoint, len(frames))
	for i, frame := range frames {
		points[i] = trajectoryPoint{
			slot:   frame.Slot,
			length: len(frame.Data),
			sendAt: frame.SendAt,
		}
	}
	return points
}

func equalTrajectory(a, b []trajectoryPoint) bool {
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

func TestCompletionTrajectoriesAreIndistinguishable(t *testing.T) {
	const (
		slots     = 5
		slotBytes = 64
	)
	plan := Plan{Slots: slots, SlotBytes: slotBytes}
	clock := newFakeClock(testStart())
	schedule := Schedule{Start: testStart(), Interval: time.Second}
	key := testKey(t, 0x31)
	result := bytes.Repeat([]byte("r"), 80)

	newRun := func(round uint32) *Encoder {
		enc, err := NewEncoder(Config{
			Plan: plan, Schedule: schedule, Round: round, Key: key, Clock: clock,
		})
		if err != nil {
			t.Fatalf("NewEncoder(round %d): %v", round, err)
		}
		return enc
	}

	runs := make(map[string][]Frame)

	t.Run("early success", func(t *testing.T) {
		clock.Set(schedule.Start)
		enc := newRun(1001)
		if err := enc.Submit(result); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		if err := enc.Finish(true); err != nil {
			t.Fatalf("Finish: %v", err)
		}
		runs["early success"] = emitAll(t, enc, clock, schedule, nil)
	})

	t.Run("success after two padding slots", func(t *testing.T) {
		clock.Set(schedule.Start)
		enc := newRun(1002)
		frames := emitAll(t, enc, clock, schedule, func(slot uint64) {
			switch slot {
			case 2:
				if err := enc.Submit(result); err != nil {
					t.Fatalf("Submit: %v", err)
				}
				if err := enc.Finish(true); err != nil {
					t.Fatalf("Finish: %v", err)
				}
			}
		})
		runs["late-ish success"] = frames
	})

	t.Run("reported failure", func(t *testing.T) {
		clock.Set(schedule.Start)
		enc := newRun(1003)
		frames := emitAll(t, enc, clock, schedule, func(slot uint64) {
			if slot == 1 {
				if err := enc.Submit(bytes.Repeat([]byte("f"), 20)); err != nil {
					t.Fatalf("Submit: %v", err)
				}
				if err := enc.Finish(false); err != nil {
					t.Fatalf("Finish: %v", err)
				}
			}
		})
		runs["reported failure"] = frames
	})

	t.Run("deadline missed", func(t *testing.T) {
		clock.Set(schedule.Start)
		enc := newRun(1004)
		frames := emitAll(t, enc, clock, schedule, func(slot uint64) {
			if slot == 0 {
				if err := enc.Submit(bytes.Repeat([]byte("m"), 10)); err != nil {
					t.Fatalf("Submit: %v", err)
				}
			}
		})
		runs["deadline missed"] = frames
	})

	t.Run("late finish", func(t *testing.T) {
		clock.Set(schedule.Start)
		enc := newRun(1005)
		_ = enc.Submit(bytes.Repeat([]byte("l"), 5))
		frames := emitAll(t, enc, clock, schedule, func(slot uint64) {
			if slot == plan.Slots-1 {
				if err := enc.Finish(true); !errors.Is(err, ErrLate) {
					t.Fatalf("Finish error = %v, want ErrLate", err)
				}
			}
		})
		runs["late finish"] = frames
	})

	var reference []trajectoryPoint
	for name, frames := range runs {
		got := trajectory(frames)
		if len(got) != slots {
			t.Fatalf("%s: %d frames, want %d", name, len(got), slots)
		}
		for i, point := range got {
			if point.slot != uint64(i) {
				t.Fatalf("%s: frame %d has slot %d", name, i, point.slot)
			}
			if point.length != slotBytes {
				t.Fatalf("%s: slot %d length = %d, want %d", name, i, point.length, slotBytes)
			}
		}
		if reference == nil {
			reference = got
			continue
		}
		if !equalTrajectory(reference, got) {
			t.Fatalf("%s: trajectory differs\n got: %+v\nwant: %+v", name, got, reference)
		}
	}
}

func TestDecryptEarlySuccess(t *testing.T) {
	plan := Plan{Slots: 5, SlotBytes: 64}
	clock := newFakeClock(testStart())
	key := testKey(t, 0x41)
	enc, schedule := testEncoder(t, plan, 2001, key, clock)
	secret := bytes.Repeat([]byte("secret"), 20) // 120 bytes < 4*48

	clock.Set(schedule.Start)
	if err := enc.Submit(secret); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := enc.Finish(true); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	frames := emitAll(t, enc, clock, schedule, nil)

	got, err := OpenFrames(plan, 2001, key, frames)
	if err != nil {
		t.Fatalf("OpenFrames: %v", err)
	}
	if !got.Success || got.Length != uint64(len(secret)) || !bytes.Equal(got.Data, secret) {
		t.Fatalf("decoded = {len:%d success:%v data:%x...}, want len:%d success:true", got.Length, got.Success, got.Data[:min(4, len(got.Data))], len(secret))
	}
}

func TestStreamingChunksAcrossSlots(t *testing.T) {
	plan := Plan{Slots: 7, SlotBytes: 64}
	clock := newFakeClock(testStart())
	key := testKey(t, 0x48)
	enc, schedule := testEncoder(t, plan, 2101, key, clock)

	parts := [][]byte{
		bytes.Repeat([]byte("a"), 10),
		bytes.Repeat([]byte("b"), 35),
		bytes.Repeat([]byte("c"), 50),
	}

	frames := emitAll(t, enc, clock, schedule, func(slot uint64) {
		switch slot {
		case 0:
			if err := enc.Submit(parts[0]); err != nil {
				t.Fatalf("Submit part 0: %v", err)
			}
		case 1:
			if err := enc.Submit(parts[1]); err != nil {
				t.Fatalf("Submit part 1: %v", err)
			}
		case 2:
			if err := enc.Submit(parts[2]); err != nil {
				t.Fatalf("Submit part 2: %v", err)
			}
			if err := enc.Finish(true); err != nil {
				t.Fatalf("Finish: %v", err)
			}
		}
	})

	want := bytes.Join(parts, nil)
	got, err := OpenFrames(plan, 2101, key, frames)
	if err != nil {
		t.Fatalf("OpenFrames: %v", err)
	}
	if !got.Success || got.Length != uint64(len(want)) || !bytes.Equal(got.Data, want) {
		for i := 0; i < len(got.Data) && i < len(want); i++ {
			if got.Data[i] != want[i] {
				t.Fatalf("decoded differs at byte %d: got %q, want %q", i, got.Data[i], want[i])
			}
		}
		t.Fatalf("decoded len=%d success=%v, want len=%d success=true", got.Length, got.Success, len(want))
	}
}

func TestSuffixLayoutAcrossResultLengths(t *testing.T) {
	plan := Plan{Slots: 8, SlotBytes: 48} // four bytes of GCM tag per slot
	clock := newFakeClock(testStart())
	key := testKey(t, 0x49)

	for i, length := range []int{0, 1, 31, 32, 33, 64, 100, 200, int(plan.EncodableBytes())} {
		t.Run(fmt.Sprintf("length_%d", length), func(t *testing.T) {
			enc, schedule := testEncoder(t, plan, uint32(2200+i), key, clock)
			payload := make([]byte, length)
			for j := range payload {
				payload[j] = byte((j*37 + 11) % 251)
			}
			clock.Set(schedule.Start)
			if err := enc.Submit(payload); err != nil {
				t.Fatalf("Submit: %v", err)
			}
			if err := enc.Finish(true); err != nil {
				t.Fatalf("Finish: %v", err)
			}
			frames := emitAll(t, enc, clock, schedule, nil)
			got, err := OpenFrames(plan, uint32(2200+i), key, frames)
			if err != nil {
				t.Fatalf("OpenFrames: %v", err)
			}
			if !got.Success || got.Length != uint64(length) || !bytes.Equal(got.Data, payload) {
				t.Fatalf("decoded len=%d success=%v, want len=%d success=true", got.Length, got.Success, length)
			}
		})
	}
}

func TestSuffixLayoutForEveryCompletionSlot(t *testing.T) {
	plan := Plan{Slots: 8, SlotBytes: 48}
	clock := newFakeClock(testStart())
	key := testKey(t, 0x4b)
	capacity := int(plan.DataBytesPerSlot())

	for finishAt := uint64(0); finishAt < plan.Slots-1; finishAt++ {
		t.Run(fmt.Sprintf("slot_%d", finishAt), func(t *testing.T) {
			remainingSlots := int(plan.Slots - 1 - finishAt)
			length := remainingSlots * capacity
			payload := bytes.Repeat([]byte{byte(finishAt + 1)}, length)
			round := uint32(2300 + finishAt)
			enc, schedule := testEncoder(t, plan, round, key, clock)
			clock.Set(schedule.Start)

			frames := emitAll(t, enc, clock, schedule, func(slot uint64) {
				if slot != finishAt {
					return
				}
				if err := enc.Submit(payload); err != nil {
					t.Fatalf("Submit: %v", err)
				}
				if err := enc.Finish(true); err != nil {
					t.Fatalf("Finish: %v", err)
				}
			})

			got, err := OpenFrames(plan, round, key, frames)
			if err != nil {
				t.Fatalf("OpenFrames: %v", err)
			}
			if !got.Success || got.Length != uint64(length) || !bytes.Equal(got.Data, payload) {
				t.Fatalf("decoded len=%d success=%v, want len=%d success=true", got.Length, got.Success, length)
			}
		})
	}
}

func TestDecryptFailureAndDeadline(t *testing.T) {
	plan := Plan{Slots: 4, SlotBytes: 64}
	clock := newFakeClock(testStart())
	key := testKey(t, 0x42)
	enc, schedule := testEncoder(t, plan, 2002, key, clock)
	partial := bytes.Repeat([]byte("p"), 17)

	clock.Set(schedule.Start)
	if err := enc.Submit(partial); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	clock.Set(schedule.SlotTime(1))
	if err := enc.Finish(false); err != nil {
		t.Fatalf("Finish(false): %v", err)
	}
	frames := emitAll(t, enc, clock, schedule, nil)

	got, err := OpenFrames(plan, 2002, key, frames)
	if err != nil {
		t.Fatalf("OpenFrames: %v", err)
	}
	if got.Success || got.Length != uint64(len(partial)) || !bytes.Equal(got.Data, partial) {
		t.Fatalf("decoded = {len:%d success:%v}, want len:%d success:false", got.Length, got.Success, len(partial))
	}

	clock2 := newFakeClock(testStart())
	key2 := testKey(t, 0x43)
	enc2, schedule2 := testEncoder(t, plan, 2003, key2, clock2)
	if err := enc2.Submit(partial); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	frames2 := emitAll(t, enc2, clock2, schedule2, nil)

	got2, err := OpenFrames(plan, 2003, key2, frames2)
	if err != nil {
		t.Fatalf("OpenFrames: %v", err)
	}
	if got2.Success || got2.Length != 0 || len(got2.Data) != 0 {
		t.Fatalf("deadline-miss decoded = {len:%d success:%v}, want empty failure", got2.Length, got2.Success)
	}
}

func TestNewEncoderRejectsInvalidInputs(t *testing.T) {
	schedule := Schedule{Start: testStart(), Interval: time.Second}
	key := testKey(t, 0x4a)

	if _, err := NewEncoder(Config{
		Plan: Plan{Slots: 2, SlotBytes: MinSlotBytes - 1}, Schedule: schedule, Round: 2102, Key: key,
	}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("small-slot error = %v, want ErrInvalidPlan", err)
	}
	if _, err := NewEncoder(Config{
		Plan: Plan{Slots: 2, SlotBytes: 64}, Schedule: Schedule{Start: testStart()}, Round: 2103, Key: key,
	}); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("zero-interval error = %v, want ErrInvalidSchedule", err)
	}
	if _, err := NewEncoder(Config{
		Plan: Plan{Slots: 2, SlotBytes: 64}, Schedule: schedule, Round: 2104, Key: nil,
	}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("nil-key error = %v, want ErrInvalidKey", err)
	}
}

func TestFrameAuthenticationAndAADBinding(t *testing.T) {
	plan := Plan{Slots: 3, SlotBytes: 64}
	raw := bytes.Repeat([]byte{0x51}, 32)
	binary.BigEndian.PutUint32(raw[:4], testKeySequence.Add(1))
	key, err := NewKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	clock := newFakeClock(testStart())
	enc, schedule := testEncoder(t, plan, 3001, key, clock)
	clock.Set(schedule.Start)
	if err := enc.Submit([]byte("authenticated")); err != nil {
		t.Fatal(err)
	}
	if err := enc.Finish(true); err != nil {
		t.Fatal(err)
	}
	frames := emitAll(t, enc, clock, schedule, nil)

	block, err := aes.NewCipher(raw)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}

	correctAAD := AssociatedData(plan, 3001, 0)
	if _, err := gcm.Open(nil, NonceFor(3001, 0), frames[0].Data, correctAAD); err != nil {
		t.Fatalf("correct AAD did not authenticate: %v", err)
	}

	for _, tc := range []struct {
		name string
		aad  []byte
	}{
		{name: "round", aad: AssociatedData(plan, 3002, 0)},
		{name: "plan slots", aad: AssociatedData(Plan{Slots: 4, SlotBytes: 64}, 3001, 0)},
		{name: "plan length", aad: AssociatedData(Plan{Slots: 3, SlotBytes: 65}, 3001, 0)},
		{name: "slot", aad: AssociatedData(plan, 3001, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := gcm.Open(nil, NonceFor(3001, 0), frames[0].Data, tc.aad); err == nil {
				t.Fatal("wrong AAD unexpectedly authenticated")
			}
		})
	}

	tampered := append([]byte(nil), frames[0].Data...)
	tampered[len(tampered)-1] ^= 0x80
	if _, err := gcm.Open(nil, NonceFor(3001, 0), tampered, correctAAD); err == nil {
		t.Fatal("tampered ciphertext unexpectedly authenticated")
	}
}

func TestRoundReuseIsRejectedAcrossKeyHandles(t *testing.T) {
	plan := Plan{Slots: 2, SlotBytes: 32}
	schedule := Schedule{Start: testStart(), Interval: time.Second}
	raw := bytes.Repeat([]byte{0x61}, 32)
	binary.BigEndian.PutUint32(raw[:4], testKeySequence.Add(1))
	key, err := NewKey(raw)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewEncoder(Config{Plan: plan, Schedule: schedule, Round: 4001, Key: key}); err != nil {
		t.Fatalf("first NewEncoder: %v", err)
	}
	if _, err := NewEncoder(Config{Plan: plan, Schedule: schedule, Round: 4001, Key: key}); !errors.Is(err, ErrRoundReuse) {
		t.Fatalf("same handle error = %v, want ErrRoundReuse", err)
	}

	otherHandle, err := NewKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewEncoder(Config{Plan: plan, Schedule: schedule, Round: 4001, Key: otherHandle}); !errors.Is(err, ErrRoundReuse) {
		t.Fatalf("same raw key error = %v, want ErrRoundReuse", err)
	}

	if _, err := NewEncoder(Config{Plan: plan, Schedule: schedule, Round: 4002, Key: key}); err != nil {
		t.Fatalf("different round was rejected: %v", err)
	}
}

func TestSubmissionLimitsAndTerminalStates(t *testing.T) {
	plan := Plan{Slots: 4, SlotBytes: 64}
	clock := newFakeClock(testStart())
	key := testKey(t, 0x71)
	enc, _ := testEncoder(t, plan, 5001, key, clock)

	if err := enc.Submit(make([]byte, int(plan.EncodableBytes())+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over-capacity Submit error = %v, want ErrTooLarge", err)
	}
	if err := enc.Finish(true); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if err := enc.Submit([]byte("x")); !errors.Is(err, ErrFinished) {
		t.Fatalf("Submit after Finish error = %v, want ErrFinished", err)
	}
	if err := enc.Finish(false); !errors.Is(err, ErrAlreadyFinished) {
		t.Fatalf("second Finish error = %v, want ErrAlreadyFinished", err)
	}

	enc2, schedule2 := testEncoder(t, plan, 5002, testKey(t, 0x72), clock)
	clock.Set(schedule2.Start)
	for slot := uint64(0); slot < plan.Slots-1; slot++ {
		if _, err := enc2.EmitNext(); err != nil {
			t.Fatal(err)
		}
	}
	if err := enc2.Submit(make([]byte, int(plan.DataBytesPerSlot())+1)); err != nil {
		t.Fatalf("Submit before Finish: %v", err)
	}
	if err := enc2.Finish(true); !errors.Is(err, ErrLate) {
		t.Fatalf("Finish without a data slot error = %v, want ErrLate", err)
	}
}

func TestFinalSlotOnlyPlan(t *testing.T) {
	plan := Plan{Slots: 1, SlotBytes: MinSlotBytes}
	clock := newFakeClock(testStart().Add(-time.Second))
	key := testKey(t, 0x81)
	enc, schedule := testEncoder(t, plan, 6001, key, clock)
	if err := enc.Finish(true); err != nil {
		t.Fatalf("Finish before start: %v", err)
	}
	clock.Set(schedule.Start)
	frame, err := enc.EmitNext()
	if err != nil {
		t.Fatalf("EmitNext: %v", err)
	}
	if len(frame.Data) != MinSlotBytes {
		t.Fatalf("frame length = %d, want %d", len(frame.Data), MinSlotBytes)
	}
	got, err := OpenFrames(plan, 6001, key, []Frame{frame})
	if err != nil {
		t.Fatalf("OpenFrames: %v", err)
	}
	if !got.Success || got.Length != 0 || len(got.Data) != 0 {
		t.Fatalf("decoded = {len:%d success:%v}, want empty success", got.Length, got.Success)
	}
}

func TestEmitExhaustionKeepsFrameCountFixed(t *testing.T) {
	plan := Plan{Slots: 2, SlotBytes: 32}
	clock := newFakeClock(testStart())
	enc, schedule := testEncoder(t, plan, 7001, testKey(t, 0x91), clock)
	frames := emitAll(t, enc, clock, schedule, nil)
	if len(frames) != int(plan.Slots) {
		t.Fatalf("frames = %d, want %d", len(frames), plan.Slots)
	}
	if _, err := enc.EmitNext(); !errors.Is(err, ErrEmitted) {
		t.Fatalf("extra EmitNext error = %v, want ErrEmitted", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
