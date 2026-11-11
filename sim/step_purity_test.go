package sim

import "testing"

func TestStepDoesNotMutateInput(t *testing.T) {
	s := InitialState(5)
	before := s.Hash()
	_ = Step(s, ButtonPunch, ButtonKick)
	after := s.Hash()
	if before != after {
		t.Fatal("Step must not mutate its input state")
	}
}

func TestRepeatedStepStable(t *testing.T) {
	a := InitialState(5)
	b := InitialState(5)
	a2 := Step(a, ButtonRight, ButtonLeft)
	b2 := Step(b, ButtonRight, ButtonLeft)
	if a2.Hash() != b2.Hash() {
		t.Fatal("same Step inputs must give same output")
	}
}
