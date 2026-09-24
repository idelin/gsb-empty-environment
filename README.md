# freqsketch — 衰减频率草图 + 准入控制热项表

纯 Go 标准库实现。分片 count-min 草图，周期衰减，准入表按"候选 vs 被淘汰者"
比较决定准入，全部依赖（数据源、时钟、失败）可注入，无远程缓存。

## 设计

- **Sketch**（`sketch.go`）：分片 × 行 × 桶的计数草图。固定哈希 seed
  （`DefaultSeed`，编译期常量），FNV-1a 哈希；测试可注入自定义 `Hasher`
  （包括强制碰撞）。计数**饱和**于 `MaxCount`，绝不溢出回绕。
- **衰减**：`AdvanceEpoch()` 声明逻辑衰减边界；每个计数格惰性记录 epoch，
  读写时按 `curEpoch - cellEpoch` 右移补衰减。因此**衰减与新增事件的任意
  物理交错都等价于声明的逻辑顺序**（`TestDecayInterleavingEquivalence`）。
  无符号右移 + 饱和加 ⇒ 计数永不为负、永不溢出。
- **Table**（`table.go`）：字节额度准入表。候选的草图估计与将被淘汰的
  最低频 occupant 比较，赢则淘汰对方并准入，输则拒绝且表不变。准入、
  淘汰、额度扣减在同一把锁内**原子**完成；决策两阶段（先模拟后应用），
  拒绝不留半成品。平频按 key 字典序淘汰，决策确定可复现。
- **Pipeline**（`pipeline.go`）：事件按 ID 去重（重复投递幂等）；时钟、
  数据源、失败点全部可注入；`Checkpoint`/`Restore` 快照全量状态，恢复后
  重放产生**逐位相同**的决策；`Close` 幂等且每个资源恰好释放一次。

## 不变量（均有测试守护）

- 估计是精确（同衰减）频数的**上界**，永不低于真值，也绝不伪称精确。
- `0 <= 计数 <= MaxCount`；`0 <= 表用量 <= 字节额度`。
- 强制哈希碰撞只放大估计（上界性质保持），不破坏值正确性。
- 注入失败的事件原子中止，可干净重试。

## 运行

```sh
go test ./...        # 全部测试（建议加 -race）
go run ./cmd/demo    # Zipf 流上演示：估计 vs 精确衰减参考
```

## 样例输出（`go run ./cmd/demo`，10 万事件 / Zipf(1.2) / 49 个衰减纪元）

```
stream: 100000 events, 999 distinct keys, 49 decay epochs

top-10 (decayed exact vs sketch upper-bound estimate):
  item-0     exact=916    estimate=916
  item-1     exact=402    estimate=402
  ...
total absolute error: 3 (0.08% of decayed stream mass)

admission table: 20 entries, 400/400 bytes used
  item-0     freq>=916    bytes=20
  ...
```

## 测试结果（`go test -race ./...`）

21 个测试全部通过，包括：衰减交错等价、饱和不溢出、强制碰撞值正确、
误差上界（20 万事件总绝对误差 62，占 0.03%）、分片并发无丢失、
56 条重复投递调度全部收敛、checkpoint 恢复决策一致、失败注入原子性、
资源恰好释放一次、紧额度下 10/10 热项保留。
