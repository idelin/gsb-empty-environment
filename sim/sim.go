// Package sim 是服务端与客户端共用的确定性格斗模拟层。
//
// 所有运算只使用整数（定点数）与确定性的输入序列，不依赖 goroutine、
// 系统时间或 map 遍历顺序，因此相同的初始状态与输入序列在任意机器上
// 都会产生逐帧一致的状态与哈希。
package sim

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
)

// 定点数比例：1 个世界单位 = Scale 个内部单位。
const Scale = 1 << 8

const (
	ArenaHalfWidth = 8 * Scale // 中心到边界
	MaxHP          = 100
	MoveSpeed      = Scale / 2 // 每帧 0.5 个单位
	AttackRange    = 3 * Scale
	PunchDamage    = 6
	KickDamage     = 10
	PunchCooldown  = 8 // 帧
	KickCooldown   = 16
	PunchStun      = 6
	KickStun       = 12
)

// 输入按钮位掩码。
const (
	ButtonLeft  uint8 = 1 << 0
	ButtonRight uint8 = 1 << 1
	ButtonPunch uint8 = 1 << 2
	ButtonKick  uint8 = 1 << 3
)

// Input 是某一帧一名玩家的输入。
type Input struct {
	Frame   int
	Player  int
	Buttons uint8
}

// Fighter 是一名格斗家的完整状态。
type Fighter struct {
	X        int // 定点横坐标，0 为场地中心
	HP       int
	Facing   int // +1 朝右，-1 朝左
	Cooldown int // 攻击冷却剩余帧
	Stun     int // 受击硬直剩余帧
}

// State 是一帧开始（或结束）时整个对局的状态。
type State struct {
	Frame   int
	Fighter [2]Fighter
	RNG     uint64 // 确定性 PRNG 状态，参与哈希
}

// InitialState 返回第 0 帧的初始对局状态。
func InitialState(seed uint64) *State {
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return &State{
		Frame: 0,
		Fighter: [2]Fighter{
			{X: -3 * Scale, HP: MaxHP, Facing: +1},
			{X: +3 * Scale, HP: MaxHP, Facing: -1},
		},
		RNG: seed,
	}
}

// Clone 返回状态的深拷贝，用于快照。
func (s *State) Clone() *State {
	cp := *s
	return &cp
}

// nextRand 是确定性的 PCG 风格步进，保证两端状态逐帧一致。
func nextRand(state uint64) uint64 {
	state = state*6364136223846793005 + 1442695040888963407
	return state
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func moveFighter(f *Fighter, buttons uint8) {
	if f.Stun > 0 || f.HP <= 0 {
		return
	}
	if buttons&ButtonLeft != 0 {
		f.X -= MoveSpeed
	}
	if buttons&ButtonRight != 0 {
		f.X += MoveSpeed
	}
	f.X = clamp(f.X, -ArenaHalfWidth, ArenaHalfWidth)
}

// Step 以两名玩家在当前帧的输入把状态推进一帧。
// 输入中携带的 Frame/Player 字段不参与模拟，只按 Buttons 计算，
// 保证回滚重算时只要按钮一致结果就一致。
func Step(state *State, localButtons, remoteButtons uint8) *State {
	next := state.Clone()
	next.Frame = state.Frame + 1
	next.RNG = nextRand(state.RNG)

	buttons := [2]uint8{localButtons, remoteButtons}
	for i := range next.Fighter {
		f := &next.Fighter[i]
		if f.Cooldown > 0 {
			f.Cooldown--
		}
		if f.Stun > 0 {
			f.Stun--
		}
		moveFighter(f, buttons[i])
	}

	// 玩家 0 的攻击先结算，再结算玩家 1，顺序固定以保持确定性。
	resolveAttacks(next, buttons)

	// 依据相对位置刷新朝向，保证双方始终面向对手。
	refreshFacing(next)
	return next
}

func resolveAttacks(s *State, buttons [2]uint8) {
	type attack struct {
		attacker, victim             int
		button                       uint8
		damage, cooldown, stunFrames int
	}
	attacks := make([]attack, 0, 2)
	if s.Fighter[0].HP > 0 && s.Fighter[0].Stun == 0 && s.Fighter[0].Cooldown == 0 {
		if buttons[0]&ButtonPunch != 0 {
			attacks = append(attacks, attack{0, 1, ButtonPunch, PunchDamage, PunchCooldown, PunchStun})
		} else if buttons[0]&ButtonKick != 0 {
			attacks = append(attacks, attack{0, 1, ButtonKick, KickDamage, KickCooldown, KickStun})
		}
	}
	if s.Fighter[1].HP > 0 && s.Fighter[1].Stun == 0 && s.Fighter[1].Cooldown == 0 {
		if buttons[1]&ButtonPunch != 0 {
			attacks = append(attacks, attack{1, 0, ButtonPunch, PunchDamage, PunchCooldown, PunchStun})
		} else if buttons[1]&ButtonKick != 0 {
			attacks = append(attacks, attack{1, 0, ButtonKick, KickDamage, KickCooldown, KickStun})
		}
	}

	for _, a := range attacks {
		attacker := &s.Fighter[a.attacker]
		victim := &s.Fighter[a.victim]
		attacker.Cooldown = a.cooldown
		if victim.HP > 0 && abs(attacker.X-victim.X) <= AttackRange {
			victim.HP = clamp(victim.HP-a.damage, 0, MaxHP)
			victim.Stun = a.stunFrames
			// 确定性击退，方向由攻击者朝向决定。
			victim.X = clamp(victim.X+attacker.Facing*(Scale/2), -ArenaHalfWidth, ArenaHalfWidth)
		}
	}
}

func refreshFacing(s *State) {
	if s.Fighter[0].X <= s.Fighter[1].X {
		s.Fighter[0].Facing, s.Fighter[1].Facing = +1, -1
	} else {
		s.Fighter[0].Facing, s.Fighter[1].Facing = -1, +1
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// hashVersion 在编码格式变化时递增，避免不同版本哈希互相比较。
const hashVersion byte = 1

// Hash 返回状态的确定性 SHA-256 摘要，用于两端不同步检测。
func (s *State) Hash() [32]byte {
	var buf bytes.Buffer
	buf.WriteByte(hashVersion)
	putInt64 := func(v int) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(v))
		buf.Write(b[:])
	}
	putInt64(s.Frame)
	var rngBytes [8]byte
	binary.LittleEndian.PutUint64(rngBytes[:], s.RNG)
	buf.Write(rngBytes[:])
	for _, f := range s.Fighter {
		putInt64(f.X)
		putInt64(f.HP)
		putInt64(f.Facing)
		putInt64(f.Cooldown)
		putInt64(f.Stun)
	}
	return sha256.Sum256(buf.Bytes())
}
