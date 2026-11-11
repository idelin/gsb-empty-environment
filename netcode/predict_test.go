package netcode

import (
	"testing"

	"example.org/rollback/sim"
)

func TestPredictedRemoteBaseline(t *testing.T) {
	ep := New(0, 8, 4, sim.InitialState(1))

	// 确认帧 1=A、帧 3=C，帧 2、4 缺失。
	confirm := func(frame int, btn uint8) {
		in := sim.Input{Frame: frame, Player: 1, Buttons: btn}
		ep.remoteHistory = appendAt(ep.remoteHistory, frame, in)
		ep.remoteSeen = markSeen(ep.remoteSeen, frame)
	}
	confirm(1, sim.ButtonPunch)
	confirm(3, sim.ButtonKick)

	cases := []struct {
		frame int
		want  uint8
	}{
		{1, sim.ButtonPunch}, // 已确认
		{2, sim.ButtonPunch}, // 回扫到帧 1
		{3, sim.ButtonKick},  // 已确认
		{4, sim.ButtonKick},  // 回扫到帧 3
		{10, sim.ButtonKick}, // 远处仍回扫到帧 3
	}
	for _, c := range cases {
		got := ep.predictedRemote(c.frame).Buttons
		if got != c.want {
			t.Errorf("frame %d: got %d want %d", c.frame, got, c.want)
		}
	}
}
