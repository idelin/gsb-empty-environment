// Package netsec 在有限 SSA 图的内存访问前插入纯检测回调，同时严格保持
// 原程序的异常边与 SSA 合流语义。
//
// 核心抽象：
//   - Func/Block/Instr 构成带源节点 ID（BlockID/InstrID）与源位置 Position 的 SSA 图；
//   - Checker 是宿主提供的纯函数检测回调，只判定 (Addr,Size,Kind) 是否合法；
//   - Instrument 在每条 OpLoad/OpStore 前插入一个 OpCheck，携带原指令的
//     源节点 ID 与源位置，默认 ModeCallback 不新增块、不改终结符与 phi。
//
// 安全约束：
//   - 纯检测：OpCheck 不读写被保护内存、无可观察副作用；
//   - 拒绝策略 ModeTrap 仅在无需拆分临界边、无需改写 phi 时才在访问前
//     插入到叶子陷阱块的条件跳转，否则返回 *RejectError 拒绝该请求；
//   - SplitCriticalEdge 在必须拆边时同步把后继 phi 的前驱标签改写为新块；
//   - 插桩幂等：已存在的 OpCheck 不会重复插入，循环回边不产生重复检测。
//
// 配套 Interpreter 是与插桩逻辑解耦的独立解释器，同一引擎可跑原图与
// 插桩图，通过事件轨迹比较合法访问的返回值与异常顺序，并核对被拒写入
// 在访问前中止、内存快照零改动（无副作用）。
package netsec
