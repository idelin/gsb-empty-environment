# 存储转发中的部分地址别名

本包实现一个供 Go 安全实验宿主调用的有限流水线模型，用来说明：

> 处理秘密的代码先向私有地址 store，再从公开地址 load。预测器只比较低 6
> 位地址，可能把秘密值推测转发给后续 probe；完整地址不同带来的误配稍后
> 才回滚，但已经发射的 probe 观察不会回滚。

## 模型

- 地址 12 位，值 8 位，寄存器 16 个（`r0`..`r15`）。
- 指令只有 `store`、`load`、`xor` 常量、`probe` 和 `barrier`，最多 16 条。
- store 缓冲最多 4 项，每项保存完整地址、值和 `Committed` 标志。
- 每个周期发射一条指令。store 在下一周期提交；缓冲满时按程序顺序退出最老
  的项并写入内存。
- load 的架构值来自内存或最近的完整地址相同 store。
- 预测器从最近的、低 6 位地址相同的 store 推测转发，即使完整地址不同。
- 推测 load 在发射后最多 4 个周期解析。完整地址不匹配时，目标寄存器回滚到
  正确的架构 load 值；解析前已经执行的相关 `xor` 会重放到修正值上。
- `barrier` 在继续执行前解析全部未决 load，因此可以修复后续观察。
- `probe(reg)` 只观察 `reg & 0xf`，即 16 个观察组。已发生的观察不会回滚。
- 地址高 6 位由宿主固定。攻击者最多选择两个公开地址槽的低 6 位，并控制
  公开内存。程序中有且仅有一个 store 的值来自秘密；`Search` 会强制这一点。

`Run` 同时产生架构轨迹和推测轨迹，并保留逐周期快照。`CycleState` 中的
`ArchRegs`/`SpecRegs`、`StoreBuffer`、`Pending` 和 `Probe` 可以直接用于
逐周期参考；不能只看最终寄存器或最终内存是否相同来判定无泄漏。

## 基本用法

```go
program := netsec.Program{
    netsec.Store(netsec.FixedAddr(0x800|4), netsec.SecretValue()),
    netsec.Load(netsec.SlotAddr(0, 0x02, 4), 0),
    netsec.Probe(0),
    netsec.Barrier(),
    netsec.Probe(0),
}

public := netsec.PublicInput{
    Low6:   [2]uint8{4, 0},
    Memory: map[netsec.Addr]netsec.Value{0x080 | 4: 0x2a},
}

run, err := netsec.Run(program, netsec.Input{Secret: 0x11, PublicInput: public})
// run.Loads[0] 给出预测选择的 store、架构 store、解析周期以及回滚前后寄存器。

result, err := netsec.Search(netsec.SearchConfig{
    Program:      program,
    PublicInputs: []netsec.PublicInput{public},
    Secrets:      []netsec.Value{0x11, 0x12},
})
```

`Search` 比较公开输入相同、秘密不同的运行。`ArchLeaks` 单列架构执行本身
依赖秘密的原有漏洞；`SpecLeaks` 只包含架构 probe 轨迹相同但推测 probe 轨迹
不同的输入对。预算耗尽且尚未发现泄漏时返回 `StatusUnknown`。

`PublicDomain.Enumerate` 可枚举最多两个低 6 位地址槽和公开内存单元的候选
值，便于宿主构造 `PublicInputs`。

## 验证场景

`go test ./...` 覆盖以下情形：

- 低 6 位不相同的无别名访问：不转发、不泄漏。
- 完整地址相同的真正同址转发：最近 store 命中且不回滚。
- 最近 store 优先：较新的部分别名 store 覆盖较老的完整地址 store。
- `barrier` 修复：barrier 前的 probe 泄漏，barrier 后的 probe 观察正确。
- 两个同时未决 load：各自独立解析，一个自然解析、另一个由 barrier 解析。
- 回滚重放后续 `xor`，以及 store 缓冲容量和同地址退出顺序。
- 最终架构状态相同但逐周期 probe 轨迹不同的泄漏不会被误报为无泄漏。
