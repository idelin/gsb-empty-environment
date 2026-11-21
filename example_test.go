package gatetable_test

import (
	"fmt"

	gt "prescreen.local/ps-282b6ff8a3"
)

func ExampleCompile() {
	p := gt.Problem{
		Period: 4,
		Ports:  []gt.PortSpec{{Forbidden: make([]bool, 4)}},
		Frames: []gt.FrameSpec{{
			Name:      "be",
			Release:   0,
			Ports:     []int{0},
			Durations: []int{1},
			Deadline:  1,
		}},
		MaxSlots:        1,
		MaxTableEntries: 2,
	}
	res, err := gt.Compile(p)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(res.Offsets[0][0], res.Latency, res.SlotsUsed, res.TableEntries)
	// Output: 0 1 1 2
}
