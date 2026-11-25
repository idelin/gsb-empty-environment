// Package leaklab 在一个刻意简化的微型指令集（8 个 8 位寄存器、256 字节
// 内存、至多 64 条指令）上检测“越界读被分支预测隐藏”的推测执行泄漏。
//
// 该模型不宣称模拟任何真实 CPU：它只形式化题面约定的一次误预测语义——
// 全程至多一次条件跳转选择相反分支，推测执行至多 8 步，遇到 barrier/halt、
// 窗口用尽或推测 PC 越界即结束，随后回滚寄存器与内存但保留 probe 记录，
// 架构执行从原分支的正确后继继续。
//
// 典型用法：
//
//	prog, _ := leaklab.ParseProgram(asmText)
//	res, err := leaklab.Analyze(leaklab.Config{
//	    Program:    prog,
//	    Spec:       leaklab.LeakySpecMem(),
//	    ExecBudget: 100000, // 模拟器执行次数上限，<=0 不限
//	})
//	switch res.Status {
//	case leaklab.StatusFound:
//	    data, _ := leaklab.EncodeWitness(res.Witness) // 存档/送审
//	    err = res.Witness.Replay()                    // 独立重放校验
//	case leaklab.StatusBoundedNotFound:
//	    // 预算内全部候选已穷尽
//	case leaklab.StatusUnknown:
//	    // 预算在穷尽前耗尽，结论不可判定
//	}
//
// 状态语义严格区分“有界未发现”与“未知”：只有全部（公开组合, 秘密取值,
// 误预测站点）候选枚举完毕仍无见证才返回 StatusBoundedNotFound，预算中断
// 一律返回 StatusUnknown。
package leaklab
