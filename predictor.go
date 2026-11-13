package netsec

// 间接目标预测器模型。
//
// 分支站点由 16 位 PC 与 8 位 domainID 标识。预测表 64 项:
//   - 索引: (PC >> 2) mod 64
//   - tag:  PC 高 10 位 (PC >> 6)
//
// 每项保存 tag、目标与 2 位置信度(0..3)。
// 训练规则:
//   - 同 tag 命中: 更新目标, 置信度 +1(饱和到 3)
//   - 异 tag: 仅当置信度为 0(或项无效)才替换, 新项置信度置 1; 否则置信度 -1
//
// 预测规则: tag 匹配且置信度 >= 2 才输出预测目标。
//
// 模型 A 不含 domainID; 模型 B 索引再异或 domainID 且 tag 包含 domainID。

const (
	TableSize        = 64
	MaxConfidence    = 3 // 2 位饱和计数器上限
	PredictThreshold = 2 // 预测所需最小置信度
)

// Model 选择预测器是否隔离 domain。
type Model int

const (
	ModelA Model = iota // 不含 domainID
	ModelB              // 索引异或 domainID 且 tag 包含 domainID
)

func (m Model) String() string {
	if m == ModelB {
		return "B"
	}
	return "A"
}

// Index 计算分支站点的表索引。模型 B 在基础索引上再异或 domainID。
func Index(model Model, pc uint16, domain uint8) uint8 {
	base := (pc >> 2) % TableSize
	if model == ModelB {
		return uint8((base ^ uint16(domain)) % TableSize)
	}
	return uint8(base)
}

// Tag 计算分支站点的 tag。模型 B 的 tag 由 PC 高 10 位拼接 domainID 构成。
func Tag(model Model, pc uint16, domain uint8) uint32 {
	tag := uint32(pc >> 6)
	if model == ModelB {
		tag = tag<<8 | uint32(domain)
	}
	return tag
}

// Entry 是预测表的一项。
type Entry struct {
	Valid      bool
	Tag        uint32
	Target     uint32
	Confidence uint8
}

// Predictor 是间接目标预测器。
type Predictor struct {
	model Model
	table [TableSize]Entry
}

func NewPredictor(model Model) *Predictor { return &Predictor{model: model} }

func (p *Predictor) Model() Model { return p.model }

// Entry 返回指定索引的表项副本(供检查/测试)。
func (p *Predictor) Entry(idx uint8) Entry { return p.table[idx%TableSize] }

// State 返回整张表的值副本, 用作完整状态 BFS 的状态键。
func (p *Predictor) State() [TableSize]Entry { return p.table }

// TrainAction 描述一次训练对表项采取的动作。
type TrainAction int

const (
	ActionReplace   TrainAction = iota // 异 tag 且置信度为 0(或项无效): 替换
	ActionUpdate                       // 同 tag 命中: 更新目标并增加置信度
	ActionDecrement                    // 异 tag 且置信度 > 0: 置信度减 1
)

func (a TrainAction) String() string {
	switch a {
	case ActionUpdate:
		return "update"
	case ActionDecrement:
		return "decrement"
	default:
		return "replace"
	}
}

// TrainEvent 记录一条训练事件进入见证所需的全部信息:
// tag、索引与置信度变化。
type TrainEvent struct {
	PC         uint16
	Domain     uint8
	Target     uint32
	Index      uint8
	Tag        uint32
	Action     TrainAction
	ConfBefore uint8
	ConfAfter  uint8
}

// Train 用 (pc, domain) 站点与目标 target 训练预测器, 返回训练事件。
func (p *Predictor) Train(pc uint16, domain uint8, target uint32) TrainEvent {
	idx := Index(p.model, pc, domain)
	tag := Tag(p.model, pc, domain)
	e := &p.table[idx]
	ev := TrainEvent{
		PC: pc, Domain: domain, Target: target,
		Index: idx, Tag: tag, ConfBefore: e.Confidence,
	}
	switch {
	case e.Valid && e.Tag == tag:
		e.Target = target
		if e.Confidence < MaxConfidence {
			e.Confidence++
		}
		ev.Action = ActionUpdate
	case !e.Valid || e.Confidence == 0:
		*e = Entry{Valid: true, Tag: tag, Target: target, Confidence: 1}
		ev.Action = ActionReplace
	default:
		e.Confidence--
		ev.Action = ActionDecrement
	}
	ev.ConfAfter = e.Confidence
	return ev
}

// Predict 对 (pc, domain) 站点做预测; 仅当 tag 匹配且置信度 >= 2 时命中。
func (p *Predictor) Predict(pc uint16, domain uint8) (target uint32, ok bool) {
	e := p.table[Index(p.model, pc, domain)]
	if e.Valid && e.Tag == Tag(p.model, pc, domain) && e.Confidence >= PredictThreshold {
		return e.Target, true
	}
	return 0, false
}

// Clone 返回预测器的深拷贝。
func (p *Predictor) Clone() *Predictor {
	q := *p
	return &q
}
