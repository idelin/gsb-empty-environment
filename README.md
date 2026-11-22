沙箱热更新时，一些区域已固定，另一些区域的映射要移动，又不能让旧句柄跨代看到新租户的页。给 Go 加载宿主实现可导入的布局器。输入最多 48 页、8 个带长度和二次幂对齐的具名区间，以及保留页、仍有旧读者的页和多组形如“两个区域起点之差模 m 属于集合 S”的约束。稳定旧映射只允许同名同代复用；旧读者退出前其他对象不能覆盖其页。输出在全部合法、按区域编号区分的布局上严格均匀；另外支持按起点元组字典序查精确排名和反排名，计数用任意精度。调用者给只可向前读的随机比特流，抽取允许暂停并序列化；在任意比特处恢复必须产生相同布局和相同下一比特位置，约束或租约代次变化使旧检查点失效。不能枚举布局目录。用小页域穷举核对每个排名，另测全空间计数超出 32 位、非二次幂个布局时的拒绝采样、对称但带不同名称的区域及撤销旧读者后可用空间变化。

## 包

根目录是可直接导入的 Go 包 `layout`：

```go
import layout "prescreen.local/ddd-2324-20261122-v2"
```

## 模型

```go
cfg := layout.Config{
    Pages:      48,
    Generation: 9,
    Regions: []layout.Region{
        // 可移动：长度 3，起点必须是 2 的幂对齐。
        {Name: "heap", Length: 3, Alignment: 4},
        // 固定到显式起点。
        {Name: "tls", Length: 1, Alignment: 1, Pinned: true, Start: 40},
        // 复用同名同代的旧映射。
        {Name: "db", Length: 4, Alignment: 2, Stable: true, Generation: 7},
    },
    Reserved: []int{0, 1},
    Stable: []layout.StableMapping{
        {Name: "db", Generation: 7, Start: 20, Length: 4},
    },
    // 旧读者仍持有的页；只有同名、同代、同起止的 stable 复用可以重叠。
    Leases: []layout.Lease{
        {Name: "db", Generation: 7, Start: 20, Length: 4},
    },
    Constraints: []layout.Constraint{
        // (start[heap] - start[tls]) mod 8 ∈ {1, 2, 6}
        {A: 0, B: 1, Modulus: 8, Allowed: []uint64{1, 2, 6}},
    },
}

p, err := layout.New(cfg)
```

区间互不重叠；`Reserved` 永远不可用；`Leases` 对其余区域不可用。`Stable`
区域若找不到同名同代的 `StableMapping`，或固定/稳定起点碰到保留页、其他
租约、对齐或边界错误，`New` 会直接报错。可移动区域即使无解也会得到合法
的 planner，`Count` 返回 0。`Lease` 可用 `Start/Length` 表示连续页，也可用
`Pages` 表示显式页集；`Config.OldReaderPages` 可表示没有可复用名称的旧读者
页，这些页对任何对象都不可用。

## 精确计数、排名和反排名

```go
total := p.Count()             // *big.Int，任意精度
rank, err := p.Rank(layout.Layout{Starts: []int{12, 40, 20}})
l, err := p.Unrank(rank)
```

排名从 0 开始，顺序是起点元组 `(start[0], start[1], ...)` 的字典序。区域按编号区分；
即使两个区域形状完全相同、只有名称不同，交换两者起点也是两个不同布局。
计数使用子集/页 DP；无跨剩余区域的模约束时是多项式级 DP，有模约束时保留
必要的起点的约束前沿（frontier）并做记忆化，不生成布局目录。

## 可暂停随机抽取

```go
src := layout.NewSliceSource(randomBytes) // 也可实现 layout.BitSource
s, err := p.NewSampler(src)
l, err := s.Draw() // 源返回 ErrNeedMoreBits 时状态不变

cp, err := s.Checkpoint()
data, err := cp.MarshalBinary()

var restored layout.Checkpoint
err = restored.UnmarshalBinary(data)
resume := layout.NewSliceSourceAt(randomBytes, restored.BitPosition())
s2, err := p.Resume(restored, resume)
l2, err := s2.Draw() // 与不中断时相同，且从相同下一比特继续
```

抽样先以拒绝采样从 `[0, Count())` 取得均匀整数，再调用 `Unrank`；因此布局
严格均匀，非 2 的幂个布局时会重抽。检查点绑定完整规范化配置（包括约束、
租约及其代次）；配置变化后用旧检查点恢复会返回 `ErrStaleCheckpoint`。

`BitSource` 只要求 `NextBit() (uint8, error)`。若源同时实现
`Position() uint64`，`Resume` 会校验它正好位于检查点记录的绝对比特位置。

## 测试

```sh
go test ./...
```

测试包括：小页域上对每个排名的穷举核对、48 页 8 区域且计数超过 32 位的
全空间计数、非 2 的幂布局数的拒绝采样、在每一个比特处暂停并序列化恢复、
约束/租约代次变化使检查点失效、对称但不同名区域、撤销旧读者后的可用空间
变化，以及固定/稳定映射和保留页/租约的边界。
