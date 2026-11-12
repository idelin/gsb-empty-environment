# allocplan

供 Go 内存宿主调用的有限标签分配规划包，用确定性的虚拟时钟管理“释放后隔离”，
防止有限标签域过快复用让旧悬空指针重新有效。

## 模型与不变量

- 标签 `Tag` 是有限域，`0` 保留为空闲哨兵 `NoTag`，可分配标签编号为 `1..TagDomainSize`。
- 隔离历史按**地址单元 × 标签**保存绝对过期时刻 `history[addr][tag]`，过期条目保留不删。
- `Alloc` 只选择满足两个条件的最低编号标签：当前无存活引用，且目标区间内每个单元的隔离均已结束。
- 找不到安全标签时绝不静默复用，而是显式返回错误：
  - `ErrAllTagsBusy`：所有标签都有存活引用，等待时钟无意义；
  - `ErrQuarantined`（包装为 `*ExhaustedError`）：标签仅处于隔离期，附带可重试的最早虚拟时刻 `WaitTick`；
  - `ErrNoAddressSpace`：没有足够大的空闲区间。
  - 所有拒绝都包装 `ErrRejected`，可用 `errors.Is(err, ErrRejected)` 统一判断，且拒绝不改变任何状态。
- 宿主可用 `NextAvailableTick` 取得等待时刻，或自行决定明确拒绝；`Clock.Advance/AdvanceTo` 只能前进不能回退。

## 区间合并与拆分的历史保留

- 隔离状态绑定在地址单元上而非段对象上。
- `Split` 只增加段边界，两段各自天然保留本单元的历史，并把同一标签的存活引用计数加一，
  因此两段需分别 `Free`。
- `Free` 把标签写入该段每个单元的隔离过期时刻，再与相邻**空闲**段合并；合并不移动、
  不扩大任何单元的历史，所以“在 A 段释放的标签”不会污染从未托管过它的相邻 B 段，
  从而支持部分区间重用。
- 重复释放同一单元同一标签时，以饱和加法计算新过期时刻；历史只延后不会缩短，
  极端大 tick 钳制在 `math.MaxInt64` 而非溢出。

## 穷举验证

`*_test.go` 使用小标签域（2 个标签、3 个地址单元、隔离 2 tick）与虚拟时钟做 BFS 穷举，
覆盖深度 10 内所有可达状态（约 2.7 万个去重状态）。每个状态尝试全部分配尺寸、全部可释放段、
全部内点拆分和单 tick 推进，并与独立的逐单元参考预言机逐字段比对：

- 段边界、单元归属、标签存活计数、逐单元隔离时刻必须完全一致；
- 规划器的接受/拒绝决策、首适配位置、选中标签、等待时刻必须与预言机一致；
- 拒绝不得改变状态，任何被规划器接受的标签都不得处于隔离或存活状态（无静默复用）；
- 时钟推进把“到达隔离过期当刻”作为显式迁移，专门验证 `T-1` 拒绝、`T` 当刻可用的边界决定。

## 最小用法

```go
planner, err := allocplan.New(allocplan.Config{
    TagDomainSize: 16,
    AddressSpace:  4096,
    Quarantine:    8,
})
if err != nil { /* 配置非法 */ }

start, tag, err := planner.Alloc(64)
if errors.Is(err, allocplan.ErrRejected) {
    var exhausted *allocplan.ExhaustedError
    if errors.As(err, &exhausted) && exhausted.Waitable {
        planner.Clock().AdvanceTo(exhausted.WaitTick) // 选择等待
        start, tag, err = planner.Alloc(64)
    }
    // 否则明确拒绝，绝不复用
}
defer planner.Free(start)
```
