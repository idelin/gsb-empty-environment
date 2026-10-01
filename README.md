# sparsearr — 缺省值稀疏数组（Go / 仅标准库）

逻辑长度可达 2^40+ 的 int64 数组。大部分位置取缺省值，只把**例外位置及值**
存入真实的压缩索引（块内 varint 编码，非 map 包装）。支持范围和、点更新、
缺省值变更、范围加法（惰性层）、快照、按累计值定位（仅非负快照）。

## 值语义（位置身份 = 下标，永不改变）

```
value(p) = 例外绝对值           若 p 被显式赋值固定（例外）
         = def + segDelta(p)    否则（缺省 + 惰性层增量）
```

- **点更新 `Assign(p,v)`**：写入绝对值例外。即使 `v` 等于当前缺省也固定身份
  ——**显式 0 不是缺失**，缺省 0→2 时它保持 0，未赋值位置跟随变为 2。
- **范围加法 `RangeAdd(l,r,d)`**：只切分惰性层段（O(相交段数)），并把范围内
  的稀疏例外逐项 +d（O(范围内例外数)）；**不遍历巨大缺省区**。赋值过的位置
  同样被后续范围加法推动（惰性层叠加语义）。
- **缺省变更 `ChangeDefault(d2)`**：未固定位置**跟随**（基准平移，惰性层增量
  保留）；例外位置**不跟随**（保持绝对值）。**回收**：值等于"回收后读取值"
  （新缺省 + 该位置惰性层增量；无惰性层时即"旧例外等于新缺省"）的例外被删除
  ——回收不改变任何读取结果，回收后该位置重新跟随缺省。
- **快照 `Snapshot()`**：run/段不可变共享 + 写时复制；旧快照可继续 Get /
  RangeSum / Locate，位置身份与历史状态完整保留。
- **范围和 `RangeSum(l,r)`**：`def*(r-l)`（缺省贡献，O(1)）+ 段增量 + 例外修正。
- **累计定位 `Locate(cum)`**：返回最小下标 p 使 [0,p] 累计和 ≥ cum。
  **仅对非负快照开放**（`NonNeg()` 精确判定）；均匀缺省段用除法算术跳跃，
  绝不逐元素扫描。

## 物理编码布局（真实字节）

```
run   = { bases []uint64, blocks []block }     // 目录 + 块序列
block = { base uint64, n uint16,               // 块头 10 B
          posEnc []byte,   // uvarint 位置差分（首项相对 base 差分恒 0）
          valEnc []byte }  // zigzag-varint 绝对值
```

- 每块 ≤128 条例外；目录 `bases` 二分定位块，范围扫描只解码相交块。
- 写缓冲为排序平行切片（16 B/条），满 128 条合并进 run（单 run 整体重建，
  摊还 O(例外数/128)）；`Compact()` 可强制合并。
- `PhysicalBytes()` 逐部分统计：头 16 B + 段 24 B/个 + 目录 8 B/块
  + 块头 10 B + posEnc + valEnc + 缓冲 16 B/条。

## 复杂度（E=例外数，S=惰性层段数，B=块大小=128）

| 操作 | 复杂度 | 说明 |
|---|---|---|
| `Assign` | 摊还 O(E/B) | 缓冲插入 O(B)，周期性合并重建 run |
| `RangeAdd` | O(相交段数 + 范围内例外数) | 与区间长度无关 |
| `RangeSum` | O(相交段数 + 相交块数·B) | 缺省区 O(1) |
| `ChangeDefault` | O(E + S) | 回收扫描 + run 重建 |
| `Locate` | O(S + E) | 均匀段算术跳跃，不逐元素 |
| `Snapshot` | O(块数 + 缓冲) | 指针级共享 |

## 运行

```sh
go test -v ./...     # 随机参考验证 + 定向语义测试 + 复杂度证据
go run ./cmd/demo    # 语义演示 + 大逻辑长度证据
```

## 运行样例（go run ./cmd/demo，节选）

```
== 1. 基本语义：缺省 0 -> 2，显式 0 不是缺失 ==
缺省 0->2 后: Get(3)=0(显式0保留) Get(4)=2(跟随) Get(5)=7(例外不跟随) 例外数=2
RangeSum(0,10)=23 （含缺省贡献 8*2+0+7=23）
== 2. 回收：旧例外等于新缺省可回收，回收后重新跟随 ==
缺省 2->7 后: Get(5)=7 例外数=1（7 被回收）
缺省 7->1 后: Get(5)=1（回收位置重新跟随）Get(3)=0（显式0仍固定）
== 5. 累计定位（仅非负快照开放） ==
Locate(1) -> 下标 2 / Locate(6) -> 下标 5；引入负值后 NonNeg=false, Locate 关闭
== 6. 大逻辑长度证据（n=2^40 ≈ 1.1e12） ==
20000 次 Assign:        506ms (25µs/op)
2000 次 RangeAdd(宽至1e9): 705ms (352µs/op)
10000 次 RangeSum(宽至1e9): 111ms (11µs/op)
物理占用 188371 字节（例外 20000 个，≈9.4 B/条）vs 稠密 8.8 TB，压缩比 4.67e7 x
== 7. 编码布局 ==
块0 base=1000 n=3: 头10B + posEnc 3B + valEnc 3B = 16 B；物理合计 56 B
```

## 测试结果（go test -v ./...）

```
--- PASS: TestRandomAgainstReference      40 组随机小数组 x 500 混合操作，对照稠密参考模型
--- PASS: TestExplicitZeroIsNotMissing    显式 0 在缺省 0->2 后保持 0
--- PASS: TestDefaultChangeFollowAndReclaim  跟随/不跟随/回收/回收后重新跟随
--- PASS: TestRangeSumIncludesDefaultContribution  范围统计含缺省贡献
--- PASS: TestMixedRangeAddAndAssignLazyLayers     范围加法与点赋值混合惰性层
--- PASS: TestLocateNonNegativeOnly       累计定位 + 负值关闭
--- PASS: TestSnapshotIsolation           快照隔离
--- PASS: TestPhysicalLayoutExactBytes    56 B 物理 vs 8.8 TB 逻辑，逐字节核对
--- PASS: TestLargeLogicalLengthEvidence  n=2^40 下 2 万例外物理 188371 B，各操作耗时
PASS  ok  example.com/sparsearr
```
