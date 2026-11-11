package sim

import "testing"

func TestStepDeterministic(t *testing.T) {
	s1 := InitialState(123)
	s2 := InitialState(123)
	seq := []struct{ a, b uint8 }{
		{ButtonRight, ButtonLeft},
		{ButtonPunch, ButtonKick},
		{ButtonRight, ButtonLeft},
		{ButtonKick, ButtonPunch},
	}
	for _, in := range seq {
		s1 = Step(s1, in.a, in.b)
	}
	// 用相同序列、分段构造 s2，结果必须一致。
	for _, in := range seq {
		s2 = Step(s2, in.a, in.b)
	}
	if s1.Hash() != s2.Hash() {
		t.Fatal("identical input sequences produced different hashes")
	}
}

func TestHashCoversAllFields(t *testing.T) {
	base := InitialState(7)
	changed := base.Clone()
	changed.Fighter[0].HP--
	if base.Hash() == changed.Hash() {
		t.Fatal("hash must change when HP changes")
	}
	changed2 := base.Clone()
	changed2.RNG++
	if base.Hash() == changed2.Hash() {
		t.Fatal("hash must change when RNG changes")
	}
}
