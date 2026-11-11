package netcode

import (
	"errors"
	"fmt"

	"example.org/rollback/ring"
	"example.org/rollback/sim"
)

// ErrPaused 表示回滚目标帧已超出快照窗口，端点暂停等待对端补齐输入。
var ErrPaused = errors.New("rollback window exceeded: simulation paused waiting for remote input")

// Endpoint 是一名玩家视角的回滚同步状态机。
//
// 它本身不接触网络与时钟：调用方每个 tick 调用 AddLocalInput + AdvanceFrame
// 推进本地模拟，通过 HandleBundle 喂入对端消息，用 BuildBundle 取出待发包。
// 这种纯帧驱动结构让服务端与客户端共用同一套逻辑，也便于确定性测试。
type Endpoint struct {
	player       int
	window       int
	hashInterval int

	frame int

	localInputs   *ring.Ring[sim.Input]
	snapshots     *ring.Ring[*sim.State]
	pendingHashes *ring.Ring[HashEntry]

	localHistory  []sim.Input
	remoteHistory []sim.Input
	remoteSeen    []bool

	paused       bool
	pauseReason  error
	stateRequest int

	desyncs       []DesyncEvent
	confirmedHash map[int][32]byte

	stats Stats
}

// New 创建端点。player 为 0 或 1；window 是回滚窗口帧数；
// hashInterval 控制状态哈希交换周期（<=0 时取 8）。
func New(player, window, hashInterval int, initial *sim.State) *Endpoint {
	if player != 0 && player != 1 {
		panic(fmt.Sprintf("netcode: player must be 0 or 1, got %d", player))
	}
	if window < 1 {
		panic("netcode: rollback window must be >= 1")
	}
	if hashInterval <= 0 {
		hashInterval = 8
	}
	e := &Endpoint{
		player:       player,
		window:       window,
		hashInterval: hashInterval,
		// 快照环额外保留一帧：回滚到窗口内最旧帧 N 时需要 N-1 的边界快照。
		localInputs:   ring.New[sim.Input](window+1, 0),
		snapshots:     ring.New[*sim.State](window+2, 0),
		pendingHashes: ring.New[HashEntry](window+1, 0),
		confirmedHash: make(map[int][32]byte),
	}
	e.snapshots.Put(0, initial.Clone())
	return e
}

func (e *Endpoint) Frame() int         { return e.frame }
func (e *Endpoint) State() *sim.State  { return e.snapshots.Get(e.frame) }
func (e *Endpoint) Paused() bool       { return e.paused }
func (e *Endpoint) PauseReason() error { return e.pauseReason }
func (e *Endpoint) Stats() Stats       { return e.stats }

// RemoteConfirmed 报告某帧的远端输入是否已确认，供测试与观测使用。
func (e *Endpoint) RemoteConfirmed(frame int) bool { return e.isRemoteKnown(frame) }

// Desyncs 返回检测到的所有哈希不一致事件。
func (e *Endpoint) Desyncs() []DesyncEvent {
	out := make([]DesyncEvent, len(e.desyncs))
	copy(out, e.desyncs)
	return out
}

// AddLocalInput 录入本帧本地输入。本地输入立即生效：
// 下一次 AdvanceFrame 会用它推进模拟，无需等待网络往返。
func (e *Endpoint) AddLocalInput(buttons uint8) {
	frame := e.frame + 1
	in := sim.Input{Frame: frame, Player: e.player, Buttons: buttons}
	e.localInputs.Put(frame, in)
	e.localHistory = appendAt(e.localHistory, frame, in)
}

func appendAt(hist []sim.Input, frame int, in sim.Input) []sim.Input {
	if frame >= len(hist) {
		grown := make([]sim.Input, frame+1)
		copy(grown, hist)
		hist = grown
	}
	hist[frame] = in
	return hist
}

func markSeen(seen []bool, frame int) []bool {
	if frame >= len(seen) {
		grown := make([]bool, frame+1)
		copy(grown, seen)
		seen = grown
	}
	seen[frame] = true
	return seen
}

// isRemoteKnown 以完整历史切片为权威，判断某帧远端输入是否已确认。
// 不使用环形 bool，避免窗口滑动后的槽位别名残留。
func (e *Endpoint) isRemoteKnown(frame int) bool {
	return frame < len(e.remoteSeen) && e.remoteSeen[frame]
}

// HandleBundle 处理对端发来的消息包，幂等且乱序安全。
// 确认输入与已模拟帧的预测不同时，立即回滚到该帧重算。
func (e *Endpoint) HandleBundle(b Bundle) error {
	for _, in := range b.Inputs {
		if err := e.confirmRemote(in); err != nil {
			return err
		}
	}
	for _, h := range b.Hashes {
		e.confirmedHash[h.Frame] = h.Hash
	}
	// 输入确认可能触发回滚重算，等所有输入落地后统一复检哈希，
	// 避免在预测状态下产生瞬态不同步误报。
	for frame := range e.confirmedHash {
		e.checkHash(frame)
	}
	e.pruneConfirmedHashes()

	if b.NeedState > 0 {
		e.stateRequest = b.NeedState
	}
	if b.StateSnapshot != nil && e.paused {
		e.applySnapshot(b.StateSnapshot)
	}
	return nil
}

func (e *Endpoint) confirmRemote(in sim.Input) error {
	if e.paused {
		// 暂停期间只缓存到完整历史，恢复时按历史统一重放。
		e.remoteHistory = appendAt(e.remoteHistory, in.Frame, in)
		e.remoteSeen = markSeen(e.remoteSeen, in.Frame)
		return nil // 暂停期间只缓存，恢复时按历史统一重放
	}

	if e.isRemoteKnown(in.Frame) {
		got := e.remoteHistory[in.Frame]
		if got.Buttons == in.Buttons {
			return nil // 重复确认，幂等忽略
		}
		return fmt.Errorf("netcode: conflicting confirmed input for frame %d: %d vs %d",
			in.Frame, got.Buttons, in.Buttons)
	}

	// 先捕获该帧当前实际采用的预测输入（此时尚未标记确认）。
	needRollback := false
	if in.Frame <= e.frame {
		predicted := e.predictedRemote(in.Frame)
		needRollback = predicted.Buttons != in.Buttons
	}

	// 落地完整历史并标记为已确认。
	e.remoteHistory = appendAt(e.remoteHistory, in.Frame, in)
	e.remoteSeen = markSeen(e.remoteSeen, in.Frame)

	if in.Frame <= e.frame {
		// 该帧已用预测输入模拟过：预测错误则回滚重算。
		if needRollback {
			if err := e.rollbackTo(in.Frame); err != nil {
				return err
			}
		}
	} else {
		// 未来帧的确认输入已存好，模拟到该帧时直接使用。
	}
	return nil
}

// pruneConfirmedHashes 清理已离开快照窗口的对端哈希，避免 map 无限增长。
func (e *Endpoint) pruneConfirmedHashes() {
	oldest := e.snapshots.Oldest()
	for f := range e.confirmedHash {
		if f < oldest {
			delete(e.confirmedHash, f)
		}
	}
	// 离开窗口的帧已无法再复检，其历史不同步事件若仍存在则视为过期清除：
	// 最终状态一致性由当前窗口内的哈希与最终 oracle 校验把关。
	if len(e.desyncs) > 0 {
		kept := e.desyncs[:0]
		for _, ev := range e.desyncs {
			if ev.Frame >= oldest {
				kept = append(kept, ev)
			}
		}
		e.desyncs = kept
	}
}

// predictedRemote 返回某帧实际（或将要）采用的远端输入：
// 已确认用确认值，否则按最近一次确认输入预测（首帧之前为零输入）。
func (e *Endpoint) predictedRemote(frame int) sim.Input {
	if e.isRemoteKnown(frame) {
		return e.remoteHistory[frame]
	}
	e.stats.Predictions++
	// 预测基线必须是严格早于 frame 的最近确认输入。
	// 全局“最近确认”指针在回滚到旧帧时可能指向未来帧，故按帧回扫历史。
	// 回扫只需覆盖回滚窗口；更早的帧即便确认也无法影响当前预测窗口。
	lower := e.snapshots.Oldest()
	if lower < 1 {
		lower = 1
	}
	for f := frame - 1; f >= lower; f-- {
		if e.isRemoteKnown(f) {
			return sim.Input{Frame: frame, Player: 1 - e.player, Buttons: e.remoteHistory[f].Buttons}
		}
	}
	return sim.Input{Frame: frame, Player: 1 - e.player, Buttons: 0}
}

func (e *Endpoint) rollbackTo(frame int) error {
	// 重算从 frame-1 的边界快照起步，因此 frame-1 必须仍在窗口内。
	if frame < 1 || !e.snapshots.Contains(frame-1) {
		// 目标帧快照已被环形缓冲淘汰：暂停，请求对端的权威状态快照。
		e.paused = true
		e.pauseReason = fmt.Errorf("%w: need frame %d, oldest available %d",
			ErrPaused, frame-1, e.snapshots.Oldest())
		e.stateRequest = frame
		e.stats.Pauses++
		return e.pauseReason
	}

	e.stats.Rollbacks++
	current := e.frame
	// 恢复到目标帧的“上一帧边界”，使重算范围包含目标帧本身：
	// 目标帧的预测输入正被确认输入替换，必须重新执行。
	base := e.snapshots.Get(frame - 1).Clone()
	e.frame = frame - 1
	e.snapshots.Put(frame-1, base)

	// 从目标帧开始逐帧重算，直到追上原先的模拟进度。
	for f := frame; f <= current; f++ {
		e.stepFrame(f)
	}
	e.stats.Resimulated += current - frame + 1

	for f := range e.confirmedHash {
		if f >= frame && f <= current {
			e.checkHash(f)
		}
	}
	return nil
}

// AdvanceFrame 推进一帧。本地输入必须先通过 AddLocalInput 录入。
// 暂停期间返回 ErrPaused 且不推进；调用方继续收发消息，
// 待对端状态快照经 HandleBundle 应用后自动恢复。
func (e *Endpoint) AdvanceFrame() error {
	if e.paused {
		return e.pauseReason
	}
	target := e.frame + 1
	if target >= len(e.localHistory) {
		return fmt.Errorf("netcode: missing local input for frame %d", target)
	}
	e.stepFrame(target)
	return nil
}

// stepFrame 执行单个帧的状态转移并保存边界快照。
func (e *Endpoint) stepFrame(frame int) {
	// 本地输入优先取完整历史，保证回滚重算不受环形窗口覆盖影响。
	var local uint8
	if frame < len(e.localHistory) {
		local = e.localHistory[frame].Buttons
	} else {
		local = e.localInputs.Get(frame).Buttons
	}
	remote := e.predictedRemote(frame).Buttons

	// 玩家编号固定映射到 sim 的两个槽位，保证两端计算顺序一致。
	var next *sim.State
	current := e.snapshots.Get(frame - 1)
	if e.player == 0 {
		next = sim.Step(current, local, remote)
	} else {
		next = sim.Step(current, remote, local)
	}

	e.frame = frame
	e.snapshots.Put(frame, next)
	e.stats.FramesSimulated++

	if frame%e.hashInterval == 0 {
		e.pendingHashes.Put(frame, HashEntry{Frame: frame, Hash: next.Hash()})
	}
}

func (e *Endpoint) checkHash(frame int) {
	want, ok := e.confirmedHash[frame]
	if !ok || !e.snapshots.Contains(frame) || e.frame < frame {
		return
	}
	// 该帧远端输入尚未确认时，本地状态可能仍基于预测、稍后会回滚重算，
	// 此刻的哈希分歧只是瞬态，不能判为不同步。
	if !e.isRemoteKnown(frame) {
		return
	}
	got := e.snapshots.Get(frame).Hash()
	if got != want {
		for _, ev := range e.desyncs {
			if ev.Frame == frame {
				return
			}
		}
		e.desyncs = append(e.desyncs, DesyncEvent{Frame: frame, Local: got, Remote: want})
		return
	}
	// 哈希重新匹配（回滚已纠正早前的预测分歧）：清除该帧的过期不同步事件。
	for i := len(e.desyncs) - 1; i >= 0; i-- {
		if e.desyncs[i].Frame == frame {
			e.desyncs = append(e.desyncs[:i], e.desyncs[i+1:]...)
		}
	}
}

// applySnapshot 用对端权威快照恢复，以快照帧为新原点重建环形窗口，
// 再依据完整输入历史重放后续帧，追上恢复前的本地进度。
func (e *Endpoint) applySnapshot(snap *sim.State) {
	origin := snap.Frame
	target := e.frame

	e.localInputs = ring.New[sim.Input](e.window+1, origin)
	e.snapshots = ring.New[*sim.State](e.window+2, origin)
	e.pendingHashes = ring.New[HashEntry](e.window+1, origin)

	e.frame = origin
	e.snapshots.Put(origin, snap.Clone())

	for f := origin + 1; f <= target; f++ {
		if f < len(e.localHistory) {
			e.localInputs.Put(f, e.localHistory[f])
		}
	}

	// 用确认输入（缺失处仍走预测）逐帧重放到恢复前进度。
	for f := origin + 1; f <= target; f++ {
		e.stepFrame(f)
	}

	e.paused = false
	e.pauseReason = nil
	e.stateRequest = 0
	// 旧哈希帧多数已离开窗口，恢复后的帧会重新交换哈希。
	e.confirmedHash = make(map[int][32]byte)
	// 恢复前基于预测态记录的不同步事件已被权威快照作废，一并清除。
	e.desyncs = nil
}

// BuildBundle 取出发给对端的消息包：全量重发窗口内本地输入，
// 捎带未送达的状态哈希；暂停时附带快照请求，收到请求时附带权威快照。
func (e *Endpoint) BuildBundle() Bundle {
	// 重发范围与快照环形窗口（容量 window+1）对齐，多带一帧，
	// 保证对端回滚到窗口内任意帧时，所需输入都能在包中找到。
	oldest := e.frame - e.window - 1
	if oldest < 1 {
		oldest = 1
	}
	inputs := make([]sim.Input, 0, e.window+1)
	for f := oldest; f <= e.frame; f++ {
		if e.localInputs.Contains(f) {
			inputs = append(inputs, e.localInputs.Get(f))
		}
	}

	hashes := make([]HashEntry, 0)
	for f := oldest; f <= e.frame; f++ {
		if f%e.hashInterval == 0 && e.pendingHashes.Contains(f) {
			if entry := e.pendingHashes.Get(f); entry.Frame == f {
				hashes = append(hashes, entry)
			}
		}
	}

	b := Bundle{
		FromPlayer: e.player,
		ToPlayer:   1 - e.player,
		Inputs:     inputs,
		Hashes:     hashes,
	}
	if e.paused && e.stateRequest > 0 {
		b.NeedState = e.stateRequest
	}
	if !e.paused && e.stateRequest > 0 {
		// 请求帧可能已滑出本端窗口：返回仍持有的最旧快照，
		// 暂停端会以它为基础，用完整输入历史重放恢复。
		req := e.stateRequest
		if req > e.frame {
			req = e.frame
		}
		if req < e.snapshots.Oldest() {
			req = e.snapshots.Oldest()
		}
		b.StateSnapshot = e.snapshots.Get(req).Clone()
		e.stateRequest = 0
	}
	return b
}

// Heartbeat 在暂停无法推进时维持消息收发，语义与 BuildBundle 相同。
func (e *Endpoint) Heartbeat() Bundle {
	return e.BuildBundle()
}
