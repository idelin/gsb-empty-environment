// Package loadshed 为微服务网格治理控制面提供基于有向依赖图的动态降级与
// 自适应负载脱落（Adaptive Load Shedding）求解能力，纯标准库实现、无外部依赖。
//
// 三大能力对应三条核心规则：
//
//   - 多路径 DAG 精确守恒传播与环路检测：NewGraph 用 Kahn 拓扑排序判定 DAG，
//     含环时返回携带最短有向环的 *CycleError 直接阻断；Graph.BuildRouting 在
//     拓扑序上做线性 DP，把全部入口经多条共享路径与扇出放大汇入同一节点的负载
//     精确累加（多入口贡献线性叠加，绝不按树重复计算）。
//   - 先弱依赖降级、再严格优先级字典序脱落：Solve 按
//     CRITICAL_PLUS > CRITICAL > SHEDDABLE_PLUS > SHEDDABLE 逐档求解，
//     每档用渐进式水填得到 Max-Min 公平放行率；容量不足时优先切断
//     “单位缓解量代价”最优的 SOFT 边替代丢弃用户请求，最后再按字典序脱落，
//     并通过冗余清理得到代价最小的切边集合。
//   - 滞回防抖与平滑回升：Controller 跨 tick 维护每条 SOFT 边与每个脱落档位的
//     冷却窗、持续达标计数与恢复水位，仅在余量连续高于水位且过冷却期时按步长
//     逐步恢复，保护动作（切断/下调）则即时生效，防止恢复瞬间流量回涌引发二次震荡。
//
// 典型用法：
//
//	g, err := loadshed.NewGraph(nodes, edges) // 含环返回 *loadshed.CycleError
//	sol, err := loadshed.Solve(g)             // 静态最优降级+脱落方案
//	ctl := loadshed.NewController(g, loadshed.DefaultHysteresisConfig())
//	snap, err := ctl.Tick()                   // 每个控制周期推进一次
package loadshed
