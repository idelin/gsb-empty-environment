package digestauth

import (
	"errors"
	"testing"
)

func TestReplayWindowAcceptsOutOfOrderRejectsDuplicates(t *testing.T) {
	w := newReplayWindow(4)

	steps := []struct {
		nc   uint32
		want error
	}{
		{1, nil},
		{3, nil}, // gap delivered out of order
		{2, nil}, // fill the gap
		{4, nil},
		{2, ErrReplay},
		{3, ErrReplay},
		{5, nil}, // slides forward, nc=1 ages out
		{1, ErrStaleNonce},
		{2, ErrReplay}, // high=5, offset 3 stays inside the cap-4 window
		{8, nil},       // jump; nc=2,3,4 age out
		{4, ErrStaleNonce},
		{5, ErrReplay},     // high=8, offset 3 remains inside the window
		{2, ErrStaleNonce}, // aged out by the jump to nc=8
	}

	for _, step := range steps {
		err := w.observe(step.nc, maxUint32Counter)
		if !errors.Is(err, step.want) {
			t.Fatalf("observe(%d) = %v, want %v", step.nc, err, step.want)
		}
	}
}

func TestReplayWindowWrapAround(t *testing.T) {
	w := newReplayWindow(8)

	if err := w.observe(5, 5); err != nil {
		t.Fatalf("observe at limit: %v", err)
	}
	if err := w.observe(6, 5); !errors.Is(err, ErrCounterWrapped) {
		t.Fatalf("observe over limit = %v, want ErrCounterWrapped", err)
	}
	// The rejected value must not be recorded: repeating the limit value is a
	// replay rather than a wrap.
	if err := w.observe(5, 5); !errors.Is(err, ErrReplay) {
		t.Fatalf("limit value after wrap attempt = %v, want ErrReplay", err)
	}
}

func TestReplayWindowUint32Boundaries(t *testing.T) {
	w := newReplayWindow(2)
	if err := w.observe(maxUint32Counter-1, maxUint32Counter); err != nil {
		t.Fatalf("observe near max: %v", err)
	}
	if err := w.observe(maxUint32Counter, maxUint32Counter); err != nil {
		t.Fatalf("observe at max: %v", err)
	}
	if err := w.observe(maxUint32Counter, maxUint32Counter); !errors.Is(err, ErrReplay) {
		t.Fatalf("duplicate max = %v, want ErrReplay", err)
	}
}
