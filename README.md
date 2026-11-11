# 重传计时器符号区域

握手协议的重传与过期计时器交错可能导致授权超期。写供Go验证宿主调用的定时状态机包，至多三只时钟，守卫为整数常数比较，动作可归零，常数不超过十；按区域等价探索错误状态，返回含具体延迟的见证。用离散边界加微小偏移的参考模型核对严格与非严格边界。

## 包结构

- `types.go`：`Machine`/`Edge`/`Guard`/`Action`/`Witness` 与限制校验 `Validate`。
- `zone.go`：差界定界矩阵（DBM），支持约束、时间流逝 `up`、归零 `reset`、成员判定 `contains`。
- `explore.go`：区域等价可达性 `Explore`，输出含每跳延迟的见证；`Replay` 独立复放。
- `reference.go`：离散边界 + 微小偏移的参考模型 `ReferenceExplore`，以及边界三点探针 `ProbeBoundaries`。
- `model.go`：握手协议示例机器 `HandshakeMachine(strictPolicy bool)`，三只时钟、常量 ≤10。

## 宿主调用示例

```go
m := netsec.HandshakeMachine(false) // 非严格授权窗口 z <= 7
w, err := netsec.Explore(m)
if err != nil { /* 机器描述非法 */ }
if w.Reachable {
    if err := netsec.Replay(m, w); err != nil { /* 见证不可复放 */ }
    for _, s := range w.Steps {
        fmt.Printf("在 %s 等待 %v 后触发 %s\n",
            m.Locations[s.Location], s.Delay, s.Event)
    }
}

// 独立参考模型对拍（不使用 DBM）。
ref, err := netsec.ReferenceExplore(m, 0)
// 严格/非严格边界三点探针：c-Eps、c、c+Eps。
br, err := netsec.ProbeBoundaries(m, netsec.LocAuthorized,
    netsec.EventUseExpired, []float64{5, 10, 0}, netsec.PolicyWindow)
```

## 边界语义

- 非严格策略：好访问守卫 `z<=7`，超期守卫 `z>7`，最小超期延迟为 `7+Eps`。
- 严格策略：好访问守卫 `z<7`，超期守卫 `z>=7`，边界点 `z==7` 即超期。
- `Eps = 1e-6` 是参考模型与探针使用的微小偏移。

运行 `go test ./...` 执行握手见证、严格/非严格探针、DBM 单测与 600 台随机机器的引擎对拍。
