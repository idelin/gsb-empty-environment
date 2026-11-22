package recovery

// 小模型使用的恢复动作。
const (
	// ActRetrieve 取回同一已密封帧重投(结果不明时唯一允许的重放)。
	ActRetrieve Action = iota + 1
	// ActRotate 提升密钥代次后重新密封(新 nonce,允许新内容)。
	ActRotate
	// ActSwitchLine 切换到线路后继继续同一时隙。
	ActSwitchLine
	// ActProbe 发送重连请求,通过公开观察区分信念分支。
	ActProbe
	// ActSkip 宣告完成,不再发送。
	ActSkip
)

func actionNames() map[Action]string {
	return map[Action]string{
		ActRetrieve:   "取回已密封帧重投",
		ActRotate:     "提升密钥代次重新密封",
		ActSwitchLine: "切换到线路后继",
		ActProbe:      "发送重连请求探测",
		ActSkip:       "宣告完成",
	}
}

var (
	obsDroppedLine1 = Observation{Line: 1, Slot: 7, FrameLen: 64, Reconnect: true, Close: CloseDropped}
	obsCleanLine1   = Observation{Line: 1, Slot: 7, FrameLen: 64, Close: CloseClean}
	obsCleanLine2   = Observation{Line: 2, Slot: 7, FrameLen: 64, Close: CloseClean}
	obsProbeLine1   = Observation{Line: 1, Slot: 7, FrameLen: 0, Reconnect: true, Close: CloseNone}
	obsProbeLine2   = Observation{Line: 2, Slot: 7, FrameLen: 0, Reconnect: true, Close: CloseNone}
)

// LostAckModel 枚举情形一:丢确认。最后一帧可能已上线但确认丢失,
// 也可能根本未发送;两种隐藏历史的公开观察完全相同。取回同一已密封
// 帧重投在两种历史下都安全(对端按槽号去重),直接宣告完成则在
// 未发送的历史下无效。
func LostAckModel() *Model {
	const (
		sent State = iota + 1
		unsent
		done
	)
	return &Model{
		Name:  "丢确认",
		Start: []State{sent, unsent},
		Goal:  map[State]bool{done: true},
		Steps: map[State]map[Action][]Transition{
			sent: {
				ActRetrieve: {{To: done, Cost: 1, Obs: obsCleanLine1}},
				ActSkip:     {{To: done, Cost: 1, Obs: obsCleanLine1}},
			},
			unsent: {
				ActRetrieve: {{To: done, Cost: 1, Obs: obsCleanLine1}},
			},
		},
		StateNames:  map[State]string{sent: "帧已上线确认丢失", unsent: "帧未发送", done: "完成"},
		ActionNames: actionNames(),
	}
}

// LineSwitchModel 枚举情形二:同槽换线。掉线后原线路可能仍存活也可能
// 已失效,时隙未变。直接切换线路后继在存活历史下多浪费一帧;先发重连
// 请求探测,可凭公开观察(响应所在线路)把信念拆开再分别处理。
func LineSwitchModel() *Model {
	const (
		alive State = iota + 1
		dead
		done
	)
	return &Model{
		Name:  "同槽换线",
		Start: []State{alive, dead},
		Goal:  map[State]bool{done: true},
		Steps: map[State]map[Action][]Transition{
			alive: {
				ActRetrieve:   {{To: done, Cost: 1, Obs: obsCleanLine1}},
				ActSwitchLine: {{To: done, Cost: 2, Obs: obsCleanLine2}},
				ActProbe:      {{To: alive, Cost: 1, Obs: obsProbeLine1}},
			},
			dead: {
				ActSwitchLine: {{To: done, Cost: 1, Obs: obsCleanLine2}},
				ActProbe:      {{To: dead, Cost: 1, Obs: obsProbeLine2}},
			},
		},
		StateNames:  map[State]string{alive: "原线路存活", dead: "原线路失效", done: "完成"},
		ActionNames: actionNames(),
	}
}

// StaleReceiptModel 枚举情形三:查到旧密文。持久回执中有一份该槽的
// 已密封帧,但它可能与当前事务摘要一致(帧未上线,可直接取回重投),
// 也可能是上一事务留下的旧密文(摘要不同)。密封纪律禁止以同一 nonce
// 密封另一内容,因此摘要不符的历史下取回重投无效,只能提升密钥代次
// 重新密封。
func StaleReceiptModel() *Model {
	const (
		match State = iota + 1
		stale
		done
	)
	return &Model{
		Name:  "查到旧密文",
		Start: []State{match, stale},
		Goal:  map[State]bool{done: true},
		Steps: map[State]map[Action][]Transition{
			match: {
				ActRetrieve: {{To: done, Cost: 1, Obs: obsCleanLine1}},
				ActRotate:   {{To: done, Cost: 2, Obs: obsCleanLine1}},
			},
			stale: {
				ActRotate: {{To: done, Cost: 2, Obs: obsCleanLine1}},
			},
		},
		StateNames:  map[State]string{match: "回执匹配帧未上线", stale: "回执为旧密文", done: "完成"},
		ActionNames: actionNames(),
	}
}

// EarlyFinishModel 枚举情形四:秘密任务提前结束。隐藏状态中任务可能
// 已经完成,也可能仍有一帧待投。恢复策略不得依赖这一秘密:取回重投
// 在两种历史下都能完成,而宣告完成在仍有待投帧的历史下无效。
func EarlyFinishModel() *Model {
	const (
		finished State = iota + 1
		pending
		done
	)
	return &Model{
		Name:  "秘密任务提前结束",
		Start: []State{finished, pending},
		Goal:  map[State]bool{finished: true, done: true},
		Steps: map[State]map[Action][]Transition{
			pending: {
				ActRetrieve: {{To: done, Cost: 1, Obs: obsCleanLine1}},
				ActRotate:   {{To: done, Cost: 2, Obs: obsCleanLine1}},
			},
		},
		StateNames:  map[State]string{finished: "秘密任务已完成", pending: "仍有一帧待投", done: "完成"},
		ActionNames: actionNames(),
	}
}

// NoCommonStepModel 枚举情形五:两个隐藏状态分别有策略却没有共同首步。
// 状态一只有取回重投有效,状态二只有换代重密封有效,二者公开观察相同,
// 任何公开策略的首步都必在某一相容历史中无效,故不存在可完成的公开
// 恢复策略。
func NoCommonStepModel() *Model {
	const (
		needRetrieve State = iota + 1
		needRotate
		done
	)
	return &Model{
		Name:  "无共同首步",
		Start: []State{needRetrieve, needRotate},
		Goal:  map[State]bool{done: true},
		Steps: map[State]map[Action][]Transition{
			needRetrieve: {
				ActRetrieve: {{To: done, Cost: 1, Obs: obsCleanLine1}},
			},
			needRotate: {
				ActRotate: {{To: done, Cost: 1, Obs: obsCleanLine1}},
			},
		},
		StateNames:  map[State]string{needRetrieve: "仅可取回重投", needRotate: "仅可换代重密封", done: "完成"},
		ActionNames: actionNames(),
	}
}

// HostModel 组合宿主模型:不完整持久回执与多个可能线路后继同时出现。
// 初始信念包含帧已上线、帧未发送、回执为旧密文与秘密任务已完成四种
// 隐藏历史。取回重投对旧密文历史无效;换代重密封对已上线历史无效
// (同槽不得出现第二份不同密文)。唯一共同首步是发送重连请求探测:
// 公开观察按响应线路把信念拆开,匹配分支取回重投,旧密文分支在线路
// 后继上换代重密封。
func HostModel() *Model {
	const (
		sent State = iota + 1
		unsent
		stale
		finished
		done
	)
	return &Model{
		Name:  "组合宿主",
		Start: []State{sent, unsent, stale, finished},
		Goal:  map[State]bool{finished: true, done: true},
		Steps: map[State]map[Action][]Transition{
			sent: {
				ActRetrieve: {{To: done, Cost: 1, Obs: obsCleanLine1}},
				ActProbe:    {{To: sent, Cost: 1, Obs: obsProbeLine1}},
			},
			unsent: {
				ActRetrieve: {{To: done, Cost: 1, Obs: obsCleanLine1}},
				ActRotate:   {{To: done, Cost: 2, Obs: obsCleanLine1}},
				ActProbe:    {{To: unsent, Cost: 1, Obs: obsProbeLine1}},
			},
			stale: {
				ActRotate:     {{To: done, Cost: 2, Obs: obsCleanLine2}},
				ActSwitchLine: {{To: done, Cost: 3, Obs: obsCleanLine2}},
				ActProbe:      {{To: stale, Cost: 1, Obs: obsProbeLine2}},
			},
		},
		StateNames: map[State]string{
			sent:     "帧已上线确认丢失",
			unsent:   "帧未发送",
			stale:    "回执为旧密文",
			finished: "秘密任务已完成",
			done:     "完成",
		},
		ActionNames: actionNames(),
	}
}

// Scenario 把一个名字与一个模型绑定,便于批量枚举。
type Scenario struct {
	Name  string
	Model *Model
}

// Scenarios 返回任务要求枚举的五个小模型情形。
func Scenarios() []Scenario {
	return []Scenario{
		{"丢确认", LostAckModel()},
		{"同槽换线", LineSwitchModel()},
		{"查到旧密文", StaleReceiptModel()},
		{"秘密任务提前结束", EarlyFinishModel()},
		{"无共同首步", NoCommonStepModel()},
	}
}
