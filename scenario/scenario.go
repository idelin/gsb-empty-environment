// Package scenario 是回滚同步的端到端验证台：
// 两个端点经由带随机延迟与丢包的中继互通，跑满固定帧数后，
// 校验两端最终状态逐字节一致，并与无网络的确定性 oracle 模拟互验。
package scenario

import (
	"errors"
	"fmt"

	"example.org/rollback/netcode"
	"example.org/rollback/netem"
	"example.org/rollback/sim"
)

// Config 描述一次验证运行的参数。
type Config struct {
	Frames       int     // 要模拟的总帧数
	Window       int     // 回滚窗口
	HashInterval int     // 状态哈希交换周期
	Seed         uint64  // 输入与网络随机种子
	MinLatency   int     // 链路基础延迟（tick）
	Jitter       int     // 链路随机抖动上限（tick）
	LossRate     float64 // 单包丢包率
	// DrainTicks 限制停止注入输入后等待管道排空的 tick 数。
	DrainTicks int
}

// DefaultConfig 返回一组能稳定触发预测、回滚又不超出窗口的参数。
func DefaultConfig() Config {
	return Config{
		Frames:       300,
		Window:       12,
		HashInterval: 8,
		Seed:         20261111,
		MinLatency:   2,
		Jitter:       3,
		LossRate:     0.15,
		DrainTicks:   200,
	}
}

// Result 汇总一次运行的观测数据。
type Result struct {
	FinalFrame  int
	FinalHash   [32]byte
	OracleHash  [32]byte
	StatesEqual bool
	HashMatch   bool
	Desyncs     []netcode.DesyncEvent
	Stats       [2]netcode.Stats
	LinkLost    [2]int
	LinkDeliver [2]int
	Paused      [2]bool
	Inputs      [2][]sim.Input // 两名玩家的确定性输入序列（下标=帧号）
}

// Runner 封装一次运行的全部运行期对象。
type Runner struct {
	cfg   Config
	eps   [2]*netcode.Endpoint
	relay *netem.Relay
	links [2]*netem.Emulator
	// inputs[p][f] 为玩家 p 在第 f 帧的输入，f 从 1 开始。
	inputs [2][]sim.Input
	tick   int
}

// New 按配置构造验证台，两端共享相同初始状态与随机种子。
func New(cfg Config) *Runner {
	initial := sim.InitialState(cfg.Seed)
	r := &Runner{cfg: cfg}
	for p := 0; p < 2; p++ {
		r.eps[p] = netcode.New(p, cfg.Window, cfg.HashInterval, initial)
		r.links[p] = netem.NewEmulator(cfg.MinLatency, cfg.Jitter, cfg.LossRate,
			cfg.Seed+uint64(p)*7919+1)
		r.inputs[p] = generateInputs(cfg.Frames, p, cfg.Seed)
	}
	r.relay = netem.NewRelay(r.links)
	return r
}

// generateInputs 生成确定性的输入序列。使用独立 PCG，
// 保证同一 seed 下每次运行完全一致，且两端互不相关。
func generateInputs(frames, player int, seed uint64) []sim.Input {
	rng := randPCG(seed ^ uint64(player+1)*0x100000001b3)
	out := make([]sim.Input, frames+1)
	for f := 1; f <= frames; f++ {
		out[f] = sim.Input{Frame: f, Player: player, Buttons: randomButtons(rng)}
	}
	return out
}

// pcg 是测试台本地的轻量确定性 PRNG，避免与模拟内部 RNG 耦合。
type pcg struct{ state uint64 }

func randPCG(seed uint64) *pcg {
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return &pcg{state: seed}
}

func (p *pcg) next() uint64 {
	p.state = p.state*6364136223846793005 + 1442695040888963407
	// xorshift 混合，改善高位质量。
	x := p.state
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	return x
}

func (p *pcg) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(p.next() % uint64(n))
}

func (p *pcg) float64() float64 {
	return float64(p.next()>>11) / float64(uint64(1)<<53)
}

// randomButtons 以受限概率生成按钮组合，保证移动与攻击都会出现。
func randomButtons(rng *pcg) uint8 {
	var b uint8
	if rng.float64() < 0.45 {
		if rng.float64() < 0.5 {
			b |= sim.ButtonLeft
		} else {
			b |= sim.ButtonRight
		}
	}
	switch rng.intn(10) {
	case 0:
		b |= sim.ButtonPunch
	case 1:
		b |= sim.ButtonKick
	}
	return b
}

// Tick 推进一个时钟 tick，顺序与回滚网络代码的参考双端驱动严格一致：
// 两端先各推进一帧、生成并发送本帧消息，然后投递中继此刻到达的消息。
func (r *Runner) Tick() {
	r.tick++

	for p := 0; p < 2; p++ {
		ep := r.eps[p]
		if ep.Paused() {
			continue
		}
		target := ep.Frame() + 1
		if target > r.cfg.Frames {
			continue // 已跑满，停止注入新帧
		}
		ep.AddLocalInput(r.inputs[p][target].Buttons)
		if err := ep.AdvanceFrame(); err != nil && !errors.Is(err, netcode.ErrPaused) {
			panic(fmt.Sprintf("scenario: endpoint %d frame %d: %v", p, target, err))
		}
	}

	for p := 0; p < 2; p++ {
		r.relay.Forward(r.eps[p].BuildBundle())
	}

	delivered := r.relay.Tick()
	for p := 0; p < 2; p++ {
		for _, b := range delivered[p] {
			_ = r.eps[p].HandleBundle(b)
		}
	}
}

// Run 执行完整运行并返回校验结果。
func (r *Runner) Run() (*Result, error) {
	// 注入阶段：至少跑 Frames 个 tick。
	for r.tick < r.cfg.Frames {
		r.Tick()
	}

	// 排空阶段：等到两端都到达目标帧、链路清空且一段时间没有新回滚。
	stable := 0
	lastRollback := [2]int{}
	for i := 0; i < r.cfg.DrainTicks; i++ {
		r.Tick()
		converged := r.eps[0].Frame() == r.cfg.Frames &&
			r.eps[1].Frame() == r.cfg.Frames &&
			!r.eps[0].Paused() && !r.eps[1].Paused()
		quiet := r.links[0].InFlight()+r.links[1].InFlight() == 0
		rb := [2]int{r.eps[0].Stats().Rollbacks, r.eps[1].Stats().Rollbacks}
		changed := rb != lastRollback
		lastRollback = rb
		if converged && quiet {
			if !changed {
				stable++
			} else {
				stable = 0
			}
			if stable >= 5 {
				break
			}
		}
	}

	return r.buildResult()
}

func (r *Runner) buildResult() (*Result, error) {
	// 排空必须保证两端的全部远端输入都已确认，否则最终状态仍含预测成分。
	for p := 0; p < 2; p++ {
		for f := 1; f <= r.cfg.Frames; f++ {
			if !r.eps[p].RemoteConfirmed(f) {
				return nil, fmt.Errorf("scenario: endpoint %d missing confirmed remote input for frame %d after drain", p, f)
			}
		}
	}
	res := &Result{
		FinalFrame: r.eps[0].Frame(),
		Inputs:     r.inputs,
	}
	for p := 0; p < 2; p++ {
		res.Stats[p] = r.eps[p].Stats()
		res.LinkLost[p] = r.links[p].Lost()
		res.LinkDeliver[p] = r.links[p].Delivered()
		res.Paused[p] = r.eps[p].Paused()
		res.Desyncs = append(res.Desyncs, r.eps[p].Desyncs()...)
	}

	s0 := r.eps[0].State()
	s1 := r.eps[1].State()
	if s0 == nil || s1 == nil {
		return nil, errors.New("scenario: endpoint has no state")
	}
	h0, h1 := s0.Hash(), s1.Hash()
	res.FinalHash = h0
	res.StatesEqual = h0 == h1 && statesEqual(s0, s1)

	oracle := r.runOracle()
	res.OracleHash = oracle.Hash()
	res.HashMatch = h0 == res.OracleHash && h1 == res.OracleHash

	if !res.StatesEqual {
		return res, fmt.Errorf("scenario: endpoint states diverged at frame %d", s0.Frame)
	}
	if !res.HashMatch {
		return res, fmt.Errorf("scenario: endpoint state does not match oracle")
	}
	if len(res.Desyncs) != 0 {
		return res, fmt.Errorf("scenario: %d hash desync events detected", len(res.Desyncs))
	}
	return res, nil
}

// runOracle 在无网络条件下，用双方完整输入序列单遍推进确定性模拟。
func (r *Runner) runOracle() *sim.State {
	state := sim.InitialState(r.cfg.Seed)
	for f := 1; f <= r.cfg.Frames; f++ {
		b0 := r.inputs[0][f].Buttons
		b1 := r.inputs[1][f].Buttons
		state = sim.Step(state, b0, b1)
	}
	return state
}

func statesEqual(a, b *sim.State) bool {
	if a.Frame != b.Frame || a.RNG != b.RNG {
		return false
	}
	for i := range a.Fighter {
		if a.Fighter[i] != b.Fighter[i] {
			return false
		}
	}
	return true
}
