package taxcalc

import "testing"

func TestTaxForward(t *testing.T) {
	rules := Rules{
		Brackets: []Bracket{
			{Lower: 0, Upper: 1000, Rate: RatePercent(10)},
			{Lower: 1000, Upper: 2000, Rate: RatePercent(20)},
			{Lower: 2000, Upper: Inf, Rate: RatePercent(30)},
		},
	}
	cases := []struct {
		gross, tax, net int64
	}{
		{0, 0, 0},
		{500, 50, 450},
		{1000, 100, 900},
		{1500, 200, 1300},
		{2000, 300, 1700},
		{2500, 450, 2050},
	}
	for _, c := range cases {
		if got := rules.Tax(c.gross); got != c.tax {
			t.Errorf("Tax(%d) = %d, want %d", c.gross, got, c.tax)
		}
		if got := rules.Net(c.gross); got != c.net {
			t.Errorf("Net(%d) = %d, want %d", c.gross, got, c.net)
		}
	}
}

func TestRoundingPerBracket(t *testing.T) {
	// Two adjacent 50% brackets round separately at each bracket end:
	// x=4 -> round(3*1/2) + round(1*1/2) = 2 + 1 = 3, while a single
	// bracket would give round(4*1/2) = 2.
	split := Rules{Brackets: []Bracket{
		{Lower: 0, Upper: 3, Rate: Rate{1, 2}},
		{Lower: 3, Upper: Inf, Rate: Rate{1, 2}},
	}}
	single := Rules{Brackets: []Bracket{
		{Lower: 0, Upper: Inf, Rate: Rate{1, 2}},
	}}
	if got := split.Tax(4); got != 3 {
		t.Errorf("split Tax(4) = %d, want 3", got)
	}
	if got := single.Tax(4); got != 2 {
		t.Errorf("single Tax(4) = %d, want 2", got)
	}
}

func TestRoundingModes(t *testing.T) {
	mk := func(m RoundMode) Rules {
		return Rules{Mode: m, Brackets: []Bracket{
			{Lower: 0, Upper: Inf, Rate: Rate{1, 2}},
		}}
	}
	cases := []struct {
		mode       RoundMode
		gross, tax int64
	}{
		{RoundHalfUp, 1, 1}, // 0.5 -> 1
		{RoundHalfUp, 3, 2}, // 1.5 -> 2
		{RoundDown, 1, 0},
		{RoundDown, 3, 1},
		{RoundUp, 1, 1},
		{RoundUp, 2, 1},
		{RoundUp, 3, 2},
	}
	for _, c := range cases {
		if got := mk(c.mode).Tax(c.gross); got != c.tax {
			t.Errorf("mode %d Tax(%d) = %d, want %d", c.mode, c.gross, got, c.tax)
		}
	}
}

func TestZeroTaxFromDeduction(t *testing.T) {
	rules := Rules{Brackets: []Bracket{
		{Lower: 0, Upper: Inf, Rate: RatePercent(10), Deduction: 300},
	}}
	if got := rules.Tax(2000); got != 0 { // round(200) - 300 < 0
		t.Errorf("Tax(2000) = %d, want 0", got)
	}
	if got := rules.Net(2000); got != 2000 {
		t.Errorf("Net(2000) = %d, want 2000", got)
	}
	if got := rules.Tax(3005); got != 1 { // round(300.5) - 300 = 1
		t.Errorf("Tax(3005) = %d, want 1", got)
	}
}

func TestValidate(t *testing.T) {
	valid := Rules{Brackets: []Bracket{
		{Lower: 0, Upper: 100, Rate: RatePercent(10)},
		{Lower: 100, Upper: Inf, Rate: RatePercent(20)},
	}}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid rules rejected: %v", err)
	}
	invalid := []Rules{
		{}, // no brackets
		{Brackets: []Bracket{{Lower: 1, Upper: Inf, Rate: RatePercent(10)}}},    // not starting at 0
		{Brackets: []Bracket{{Lower: 0, Upper: Inf, Rate: Rate{1, 0}}}},         // zero denominator
		{Brackets: []Bracket{{Lower: 0, Upper: Inf, Rate: Rate{-1, 2}}}},        // negative rate
		{Brackets: []Bracket{{Lower: 0, Upper: 100, Rate: RatePercent(10)}}},    // bounded last bracket
		{Brackets: []Bracket{{Lower: 0, Upper: Inf}, {Lower: Inf, Upper: Inf}}}, // unbounded non-last
		{Brackets: []Bracket{{Lower: 0, Upper: 50, Rate: RatePercent(10)}, // empty bracket
			{Lower: 50, Upper: 50, Rate: RatePercent(10)}, {Lower: 50, Upper: Inf}}},
		{Brackets: []Bracket{{Lower: 0, Upper: 100, Rate: RatePercent(10)}, // gap
			{Lower: 101, Upper: Inf, Rate: RatePercent(10)}}},
		{Brackets: []Bracket{{Lower: 0, Upper: 100, Rate: RatePercent(10)}, // overlap
			{Lower: 99, Upper: Inf, Rate: RatePercent(10)}}},
		{Mode: RoundMode(99), Brackets: []Bracket{{Lower: 0, Upper: Inf}}}, // bad mode
	}
	for i, r := range invalid {
		if err := r.Validate(); err == nil {
			t.Errorf("invalid rules %d accepted", i)
		}
	}
}
