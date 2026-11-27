package bbrv2

// maxFilter is a Kathleen Nichols style three-sample windowed max filter.
//
// It is a direct Go port of the Linux BBR "minmax" filter
// (bbr_minmax_get / bbr_minmax_advance_window / bbr_minmax_running_max in
// tcp_bbr.c). Three round-tagged slots hold the maxima of the most recent
// sub-windows, the overall maximum always resides in s[0], and the window
// length is expressed in Round-Trip rounds so stale measurements age out at
// round boundaries.
const (
	maxFilterSlots  = 3
	maxFilterModulo = 4 // BBR_MINMAX_WINDOW alignment granularity
)

type maxFilterSlot struct {
	round uint32 // round in which the value was observed
	value uint64 // observed bandwidth (bytes/sec), 0 means "empty"
}

type maxFilter struct {
	window uint32 // nominal window length in rounds
	s      [maxFilterSlots]maxFilterSlot
}

func newMaxFilter(window uint32) *maxFilter {
	return &maxFilter{window: window}
}

// get returns the running maximum bandwidth estimate, which always lives in s[0].
func (f *maxFilter) get() uint64 {
	return f.s[0].value
}

// selectWindow mirrors bbr_minmax_select_window: an aligned window shrinks by
// (modulo-1) so samples expire slightly before the nominal window boundary.
func (f *maxFilter) selectWindow() uint32 {
	win := f.window
	if win != 0 && win%maxFilterModulo == 0 {
		win -= maxFilterModulo - 1
	}
	return win
}

// advanceWindow ages out slots whose timestamp is older than the window,
// demoting younger slots into the vacated position as in the reference code.
func (f *maxFilter) advanceWindow(round uint32) {
	win := f.selectWindow()
	if win == 0 {
		return
	}
	if f.s[2].value != 0 && round > f.s[2].round+win {
		f.s[2] = maxFilterSlot{}
	}
	if f.s[1].value != 0 && round > f.s[1].round+win {
		f.s[1] = f.s[2]
		f.s[2] = maxFilterSlot{}
	}
	if f.s[0].value != 0 && round > f.s[0].round+win {
		f.s[0] = f.s[1]
		f.s[1] = f.s[2]
		f.s[2] = maxFilterSlot{}
	}
}

// runningMax records meas observed in round and returns the new windowed
// maximum. Zero samples are ignored and never enter the filter.
func (f *maxFilter) runningMax(round uint32, meas uint64) uint64 {
	if meas == 0 {
		return f.get()
	}
	f.advanceWindow(round)
	win := f.selectWindow()

	if f.s[2].value == 0 || meas >= f.s[2].value {
		f.s[2] = maxFilterSlot{round: round, value: meas}
	}
	if f.s[1].value == 0 || meas >= f.s[1].value {
		f.s[1] = f.s[2]
	} else if f.s[2].value > f.s[1].value {
		f.s[1].value = f.s[2].value
	}
	if f.s[0].value == 0 || meas >= f.s[0].value {
		f.s[0] = f.s[1]
	} else if f.s[1].value > f.s[0].value {
		f.s[0].value = f.s[1].value
	}

	dt := round - f.s[0].round
	if f.window != 0 && dt > win {
		f.window = win + (dt & (maxFilterModulo - 1))
	}
	return f.get()
}

// reset clears the filter history and restores the nominal window length.
func (f *maxFilter) reset(window uint32) {
	f.window = window
	f.s = [maxFilterSlots]maxFilterSlot{}
}
