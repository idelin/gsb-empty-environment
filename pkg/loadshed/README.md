# loadshed — 强弱依赖图动态降级与优先级负载脱落求解包

纯 Go 标准库实现（无外部依赖），导入路径：

```
prescreen.local/ps-c040c43535/pkg/loadshed
```

## 数据模型

- `Node`：服务节点，含健康容量上限 `Cap` 与四档入口到达速率 `Arrival`。
- `Edge`：有向调用边 `From -> To`，含 `Fanout > 0` 调用放大倍数、`Dep`（`Hard`/`Soft`）
  与 `DegradeCost`（仅 `Soft` 边使用的轻量降级代价）。
- 优先级常量：`CriticalPlus > Critical > SheddablePlus > Sheddable`（值越小越高优）。

## 求解规则与对应实现

1. **多路径 DAG 精确守恒传播与环路检测**
   - `NewGraph` 用 Kahn 拓扑排序判定 DAG；含环时返回 `*CycleError`，其 `Nodes` 字段
     给出一条节点数最少（并列取字典序最小）的首尾闭合有向环，直接阻断后续求解。
   - `Graph.BuildRouting` 在拓扑序上做线性 DP，得到路由系数
     `Coef[p][entry][v]`：入口 `entry` 的 1 单位优先级 `p` 放行流量，经全部共享路径
     与扇出放大后在节点 `v` 上的真实负载贡献。多入口贡献在 `Propagate` 中线性叠加，
     菱形/多路径汇入被精确累加，不按树重复计算。

2. **先弱依赖降级、再严格优先级字典序脱落**
   - `Solve` 从高到低逐档求解，每档 `runTier` 用渐进式水填得到 **Max-Min 公平**放行率
     （按放行速率均等抬升，到达上限或瓶颈耗尽即冻结相关流；扇出系数作为加权约束）。
   - 容量不足时 `pickSoftEdge` 优先切断「单位缓解量降级代价 `cost/gain` 最小」的
     `SOFT` 边替代丢弃请求；切边只清零该边流量，上游节点本地负载不变（不引起上游失败）。
   - 四档目标为严格字典序最大化保留量；`pruneRedundantCuts` 在不劣化任何一档保留量的
     前提下移除冗余切边，得到降级代价最小的切边集合。
   - `Solution.FullyAdmitted=false` 表示即便切完所有可切 `SOFT` 边仍必须脱落用户请求。

3. **滞回防抖与容量恢复平滑回升**
   - `Controller` 跨 tick 维护每条 `SOFT` 边与每个脱落档位的冷却窗
     （`CooldownTicks`）、持续达标计数（`SustainTicks`）与恢复水位
     （`RecoverHeadroomRatio`）。
   - 保护动作（切断软边、下调放行比例）**即时生效**，不等冷却；恢复动作必须同时满足
     「瓶颈余量连续高于水位」与「过冷却期」，并按 `EdgeStep`/`RatioStep` 逐步回升。
   - 每一步恢复都会做容量+水位双重试算，不安全即回滚并重置持续计数，防止回涌二次过载。

## 最小示例

```go
g, err := loadshed.NewGraph(nodes, edges) // 含环返回 *loadshed.CycleError
var ce *loadshed.CycleError
if errors.As(err, &ce) { /* 阻断并上报最短环 ce.Nodes */ }

sol, err := loadshed.Solve(g)
_ = sol.CutEdges               // 建议切断的 SOFT 边编号
_ = sol.Admission              // 各入口各优先级放行比例
_ = sol.Load                   // 精确传播后的各节点总负载

ctl := loadshed.NewController(g, loadshed.DefaultHysteresisConfig())
for {
    snap, _ := ctl.Tick()      // 每个控制周期推进一次，拓扑不变、容量/速率可变
    _ = snap.EdgeScale
    _ = snap.Admission
}
```

## 验收测试

`go test ./...` 覆盖：菱形多路径扇出精确守恒、切断弱依赖保全关键流量、四档字典序严格
保护、同优先级多入口 Max-Min 公平（含扇出加权）、调用环最短拦截、恢复滞回防震荡时间线，
以及零容量/无流量等边界场景。
