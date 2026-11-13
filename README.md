# 组相联缓存探测恢复秘密索引

密码回归宿主需要判断查表实现是否让攻击者从Prime+Probe轨迹区分秘密。给Go测试宿主实现本题缓存模型与见证搜索包：地址空间0至4095字节，缓存线16字节、16组、每组2路，组号为地址除16再模16，替换为真LRU；缓存初始为空。攻击者先按给定地址序列prime，受害者执行至多16次load，地址是公开输入和一个4位秘密经题内表达式算出的结果，表达式只含常量、模4096加法、异或与查表，查表索引和值由输入显式给出且均在范围内；之后攻击者按给定地址序列probe，每次只观察hit或miss。受害者load与probe都真实更新LRU。输入给公开值集合、秘密0至15及prime/probe候选序列，搜索同一公开输入下产生不同probe位串的秘密对，输出完整逐访问组、命中和LRU状态；只有枚举全部秘密和公开集合后才可报在给定实验下不可区分。另实现逐secret轨迹分区，返回每个观察等价类，不能从不同轨迹直接声称唯一恢复。测试冲突地址、同组不同tag、prime自身重复、受害者多次访问及改变probe顺序，独立朴素缓存参考须与优化搜索逐步一致。

## 实现

缓存参数用常量 `AddressSpace=4096`、`LineSize=16`、`NumSets=16`、`Ways=2`。`Cache` 的每组按最后访问时钟维护真 LRU；命中、失效填充和淘汰都使被访问行成为 MRU。`AccessResult` 同时给出阶段、序号、地址、cache line、组号、tag、命中、被淘汰 tag、访问前后的 `SetState` 以及该组 LRU 顺序。

受害者程序是 `Program`，最多包含 `MaxVictimLoads=16` 个 `Expr`。表达式节点只支持常量、公开值、4 位秘密、模 4096 加法、异或和查表。常量与查表值必须落在 `[0,4096)`；查表索引在求值时检查，越界返回错误；`NewProgram` 与 `Program.Validate` 会拒绝非法节点、非法查表值和超限 load。也可以用 `VictimFunc` 提供自定义地址函数，搜索侧仍会校验地址和 load 数。

`Simulate` 运行单个 `(public, secret)` 实验并返回完整轨迹：`Accesses` 是按执行顺序的 prime、victim、probe 访问，`PrimeAccesses`、`VictimAccesses`、`ProbeAccesses` 是按阶段分组视图，`ProbeObservation` 是 `H`/`M` 位串，`FinalCache` 是终态全组快照。

## 搜索与结论

```go
table := make([]int, 16)
for i := range table {
    table[i] = i * 256
}
victim, _ := netsec.NewProgram(
    netsec.AddExpr(netsec.PublicExpr(), netsec.LookupExpr(netsec.SecretExpr(), table)),
)
result, _ := netsec.Search(netsec.SearchConfig{
    Publics: []int{0, 1},
    Prime:   []int{0, 256},
    Probe:   []int{0, 256},
    Victim:  victim,
})
```

`Secrets` 为 `nil` 时枚举全部 `0..15`；给出子集时只枚举子集并记录 `MissingSecrets`。`Search` 对每个公开值和每个秘密都从空缓存开始，随后在同一公开值内比较所有秘密对的 `ProbeObservation`，生成 `Witness`。

结论只有三种：

- `VerdictDistinguishable`：找到至少一对同公开值、不同 probe 位串的秘密。即使只枚举了部分秘密，这个结论也有效。
- `VerdictIndistinguishable`：仅在 `Complete=true` 时给出，即秘密 `0..15` 全部枚举、至少有一个公开值、且没有任何见证对。它只针对给定实验，不是对任意攻击者的绝对结论。
- `VerdictInconclusive`：没有见证对，但秘密或公开集合没有完整覆盖。

## 轨迹分区

`ObservationPartition` 按单个公开值的 probe 位串分组：位串相同的秘密进入同一 `ObservationClass`。`SecretPartition` 按每个秘密在全部公开值上的位串向量分组。两者都保留重复/等价轨迹，不会把一个位串类拆成多个秘密。

`ObservationPartition.Recover`、`SecretPartition.Recover`/`RecoverFromObservations` 只在完整枚举且目标类恰好含一个秘密时返回成功。`RecoverObservation` 返回候选集合，`Unique` 同样要求完整枚举。不同轨迹只说明它们属于不同观察类，不能绕过候选类直接宣称唯一恢复。

## 测试

`cache_test.go` 和 `search_test.go` 覆盖冲突地址、同组不同 tag、同 line 不同偏移、prime 重复访问、受害者多次 load、probe 顺序变化、非法表达式、完整与不完整覆盖、歧义轨迹类。独立的 `naiveCache` 使用有序切片而非优化缓存的时间戳数组，在每次访问及完整搜索轨迹上逐步比较组号、tag、命中、淘汰和 LRU。

```sh
go test ./...
```
