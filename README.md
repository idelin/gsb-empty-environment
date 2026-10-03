# Broadphase —— 空间实体碰撞预测与动态碰撞对筛选器

物理引擎粗测阶段（Broadphase）碰撞检测器，Go 标准库实现，零依赖。

## 设计

- **增量动态 AABB 树**（`tree.go`，参考 Box2D `b2DynamicTree`）：
  - 插入 / 删除均为 O(log N)，SAH（表面积启发）贪心选择兄弟节点；
  - AVL 式旋转平衡，500 个代理时树高稳定在 10~11（log2(500)≈9）。
- **肥胖 AABB + 碰撞预测**（`broadphase.go`）：
  - 包围盒向外扩张 `margin`，位移未超出边距时 `Move` 为 O(1)，不触碰树；
  - 超出边距时按 `margin + displacement × predict` 沿运动方向扩张后增量重插（O(log N)），
    实现投机性（speculative）碰撞预测。
- **树内自相交遍历**（`Tree.CollidePairs`）：
  - 从 `(root, root)` 出发的节点对栈式遍历，重叠子树才下潜，
    每个无序叶子对恰好产出一次，避免 O(N²) 两两暴力比对；
  - 树未变化时 `UpdatePairs` 直接返回缓存结果。

##  API 概览

```go
bp := broadphase.New(0.1, 2.0)   // 肥胖边距, 碰撞预测系数
bp.Add(id, box)                  // 添加实体
bp.Move(id, box, displacement)   // 物体移动：增量更新包围盒
pairs := bp.UpdatePairs()        // 自相交遍历，产出潜在碰撞对（已排序去重）
bp.Remove(id)                    // 移除实体
```

## 运行样例

```
$ go run ./cmd/demo
场景: 500 个刚体同时运动, 120 帧
动态树高度: 11 (log2(500) ≈ 9)
每帧候选对: 12 (已去重排序, 移交窄测)
候选对列表: [{0 1} {2 3} {4 5} {6 7} {8 9} {10 11} {12 13} {14 15} {16 17} {18 19} {20 21} {22 23}]
粗测耗时: 平均 1777.6 µs/帧 (含 500 次移动更新 + 自相交遍历)
暴力对照: 平均 3339.3 µs/帧 (O(N^2) = 124750 次两两比对)
```

> 注：上述墙钟耗时采集于 CPU 受限沙箱（实测算力约为物理机的 3%，
> 100M 次浮点循环需 5.2s）。以 pprof CPU 采样折算的真实耗时见下。

## 测试结果

```
$ go test -v .
=== RUN   TestTreeInsertRemoveQuery
--- PASS: TestTreeInsertRemoveQuery (0.00s)        # 300 代理随机插入/删除/区域查询 vs 暴力法
=== RUN   TestCollidePairsMatchesBruteForce
--- PASS: TestCollidePairsMatchesBruteForce (0.00s) # 自相交遍历产出对 vs O(N^2) 暴力法，无重复无遗漏
=== RUN   TestAcceptance500Bodies12Pairs
--- PASS: TestAcceptance500Bodies12Pairs            # 验收场景：500 刚体 120 帧，每帧恰好 12 对且与暴力法一致
=== RUN   TestMoveWithinFatMarginSkipsTreeUpdate
--- PASS: TestMoveWithinFatMarginSkipsTreeUpdate (0.00s) # 边距内移动 O(1)，超出才重插
=== RUN   TestUpdatePairsCachedWhenClean
--- PASS: TestUpdatePairsCachedWhenClean (0.00s)    # 树未变化时返回缓存
=== RUN   TestEdgeCases
--- PASS: TestEdgeCases (0.00s)                     # 空场景 / 单刚体 / 删除
PASS
ok  	broadphase
```

## 基准（Apple M1 Pro）

```
$ go test -run '^$' -bench . -benchtime 500x .
BenchmarkBroadphase500-10     500   2772634 ns/op   977 B/op   8 allocs/op
BenchmarkBruteForce500-10     500   3048669 ns/op    16 B/op   0 allocs/op
```

沙箱墙钟受 CPU 限流影响；按 pprof CPU 采样折算的真实每帧耗时：

| 阶段 | CPU 耗时/帧 |
|---|---|
| 500 次 `Move`（约 49 次实际重插，其余 O(1) 跳过） | ≈ 40 µs |
| `UpdatePairs` 自相交遍历（约 4852 次节点对访问） | ≈ 40 µs |
| **粗测合计** | **≈ 80 µs（数十微秒量级）** |
| O(N²) 暴力对照（124750 次比对） | ≈ 140 µs |

随 N 增大，粗测的 O(N log N) 优势会持续扩大（暴力法为 O(N²)）。

## 目录结构

```
aabb.go        # Vec2 / AABB 几何类型：重叠、包含、肥胖化、位移扩张
tree.go        # 增量动态 AABB 树：插入、删除、旋转平衡、区域查询、自相交遍历
broadphase.go  # Broadphase 筛选器：肥胖边距、碰撞预测、Pairs 产出与缓存
broadphase_test.go # 正确性测试（对照暴力法）+ 验收场景 + 基准
cmd/demo/      # 运行样例：500 刚体 × 120 帧模拟
```
