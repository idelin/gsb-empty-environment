package chronos

// Synthetic timezone tables for the zone "X/Island", used to exercise
// migrations across table versions. They contain multiple fall-back
// (overlapping wall clock) transitions.
//
// Version "2024a": base offset +1h, DST (+2h) in summer, with fall-back
// transitions in Oct 2020, Oct 2021 and Oct 2022.
// Version "2024b": identical up to and including the Oct 2021 fall-back,
// then DST is abolished (permanent +1h from 2021-10-31).

func islandSec(y int32, mo, d, h, mi, s uint8) int64 {
	return daysFromCivil(y, mo, d)*86400 + int64(h)*3600 + int64(mi)*60 + int64(s)
}

func islandTransitions(v2 bool) []Transition {
	tr := []Transition{
		{AtSec: islandSec(2020, 3, 29, 1, 0, 0), OffsetBefore: 3600, OffsetAfter: 7200},
		{AtSec: islandSec(2020, 10, 25, 1, 0, 0), OffsetBefore: 7200, OffsetAfter: 3600},
		{AtSec: islandSec(2021, 3, 28, 1, 0, 0), OffsetBefore: 3600, OffsetAfter: 7200},
		{AtSec: islandSec(2021, 10, 31, 1, 0, 0), OffsetBefore: 7200, OffsetAfter: 3600},
	}
	if !v2 {
		tr = append(tr,
			Transition{AtSec: islandSec(2022, 3, 27, 1, 0, 0), OffsetBefore: 3600, OffsetAfter: 7200},
			Transition{AtSec: islandSec(2022, 10, 30, 1, 0, 0), OffsetBefore: 7200, OffsetAfter: 3600},
		)
	}
	return tr
}

// SyntheticIslandV1 returns the "2024a" table.
func SyntheticIslandV1() *TZTable {
	t, err := NewTZTable("X/Island", "2024a", 3600, islandTransitions(false))
	if err != nil {
		panic(err)
	}
	return t
}

// SyntheticIslandV2 returns the "2024b" table (DST abolished after 2021).
func SyntheticIslandV2() *TZTable {
	t, err := NewTZTable("X/Island", "2024b", 3600, islandTransitions(true))
	if err != nil {
		panic(err)
	}
	return t
}

// SyntheticRegistry returns a registry with both synthetic versions.
func SyntheticRegistry() *Registry {
	r := NewRegistry()
	r.Register(SyntheticIslandV1())
	r.Register(SyntheticIslandV2())
	return r
}
