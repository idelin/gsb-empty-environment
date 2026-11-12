package coververify

import (
	"fmt"
	"sync"
)

// testHost 是测试用确定性状态机宿主。
//
// 它维护两个模型版本各自的账户状态注册表，模型版本之间状态完全隔离：
//   - bank/v1：余额不足的取款被拒绝，账户状态保持不变（拒绝转换）；
//   - bank/v2：允许透支，并收取 5 个单位的手续费。
//
// 所有摘要都由内容派生（见 JSONDigest/SumDigest），宿主与插件共享摘要约定，
// 从而可以构造合法回执、篡改回执和跨模型拼接回执。
type testHost struct {
	mu     sync.Mutex
	states map[string]map[Digest]testAccount // modelVersion -> prev digest -> account

	failEdges  map[TransitionKey]error  // 让指定边的重放返回错误
	panicEdges map[TransitionKey]string // 让指定边的重放 panic
	calls      map[TransitionKey]int    // 记录每条边实际触发重放的次数
}

type testAccount struct {
	Account string `json:"account"`
	Balance int    `json:"balance"`
}

type testCommand struct {
	Op     string `json:"op"`
	Amount int    `json:"amount"`
}

type testOutput struct {
	OK       bool   `json:"ok"`
	Balance  int    `json:"balance"`
	Reason   string `json:"reason,omitempty"`
	Rejected bool   `json:"rejected,omitempty"`
}

func newTestHost() *testHost {
	return &testHost{
		states:     make(map[string]map[Digest]testAccount),
		failEdges:  make(map[TransitionKey]error),
		panicEdges: make(map[TransitionKey]string),
		calls:      make(map[TransitionKey]int),
	}
}

func (h *testHost) register(model string, acct testAccount) Digest {
	h.mu.Lock()
	defer h.mu.Unlock()
	state, err := JSONDigest(acct)
	if err != nil {
		panic(err)
	}
	set := h.states[model]
	if set == nil {
		set = make(map[Digest]testAccount)
		h.states[model] = set
	}
	set[state] = acct
	return state
}

func digestOrPanic(value any) Digest {
	d, err := JSONDigest(value)
	if err != nil {
		panic(err)
	}
	return d
}

func commandDigest(op string, amount int) Digest {
	return digestOrPanic(testCommand{Op: op, Amount: amount})
}

// oracle 是适配 [OracleFunc] 的确定性转换回调，支持错误/panic 注入。
func (h *testHost) oracle(model string, prev, input Digest) ReplayResult {
	edge := TransitionKey{ModelVersion: model, PrevState: prev, Input: input}

	h.mu.Lock()
	h.calls[edge]++
	reason, panicNow := h.panicEdges[edge]
	failErr, failNow := h.failEdges[edge]
	accounts, modelKnown := h.states[model]
	var acct testAccount
	var stateKnown bool
	if modelKnown {
		acct, stateKnown = accounts[prev]
	}
	h.mu.Unlock()

	if panicNow {
		panic(reason)
	}
	if failNow {
		return ReplayResult{Err: failErr}
	}
	if !modelKnown {
		return ReplayResult{Err: fmt.Errorf("unknown model %q", model)}
	}
	if !stateKnown {
		return ReplayResult{Err: fmt.Errorf("unknown state %q in model %q", prev, model)}
	}

	// 输入摘要反查命令。测试里命令集合固定，直接尝试两种命令编码即可，
	// 未知输入摘要必须报错而不是猜测，避免为伪造回执背书。
	cmd, found := lookupCommand(input)
	if !found {
		return ReplayResult{Err: fmt.Errorf("unknown input digest %q", input)}
	}

	switch cmd.Op {
	case "deposit":
		next := testAccount{Account: acct.Account, Balance: acct.Balance + cmd.Amount}
		nextDigest := h.register(model, next)
		return ReplayResult{
			NextState: nextDigest,
			Output:    digestOrPanic(testOutput{OK: true, Balance: next.Balance}),
		}
	case "withdraw":
		switch model {
		case "bank/v1":
			if cmd.Amount > acct.Balance {
				return ReplayResult{
					NextState: prev, // 拒绝转换：状态保持不变
					Output: digestOrPanic(testOutput{
						OK: false, Balance: acct.Balance, Reason: "insufficient funds", Rejected: true,
					}),
					Rejected: true,
				}
			}
		case "bank/v2":
			// v2 允许透支，收取固定手续费。
		default:
			return ReplayResult{Err: fmt.Errorf("unsupported model %q", model)}
		}
		nextBalance := acct.Balance - cmd.Amount
		if model == "bank/v2" && acct.Balance < cmd.Amount {
			nextBalance -= 5
		}
		next := testAccount{Account: acct.Account, Balance: nextBalance}
		nextDigest := h.register(model, next)
		return ReplayResult{
			NextState: nextDigest,
			Output:    digestOrPanic(testOutput{OK: true, Balance: next.Balance}),
		}
	}
	return ReplayResult{Err: fmt.Errorf("unsupported op %q", cmd.Op)}
}

var knownCommands = []testCommand{
	{Op: "deposit", Amount: 10},
	{Op: "deposit", Amount: 20},
	{Op: "withdraw", Amount: 15},
	{Op: "withdraw", Amount: 100},
}

func lookupCommand(input Digest) (testCommand, bool) {
	for _, cmd := range knownCommands {
		if commandDigest(cmd.Op, cmd.Amount) == input {
			return cmd, true
		}
	}
	return testCommand{}, false
}

// legitReceipt 用宿主重放结果构造一份内容合法的回执（模拟插件如实上报）。
func legitReceipt(host *testHost, model string, prev, input Digest) Receipt {
	replay := host.oracle(model, prev, input)
	if replay.Err != nil {
		panic(replay.Err)
	}
	return Receipt{
		ModelVersion: model,
		PrevState:    prev,
		Input:        input,
		Output:       replay.Output,
	}
}
