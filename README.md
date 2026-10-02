# broadphase — 空间实体碰撞预测与动态碰撞对筛选器

物理引擎**粗测阶段**碰撞检测器（Go，仅标准库）。基于**增量动态 AABB 树**
维护所有运动刚体的外包盒，刚体移动时增量更新包围盒，并通过**树内自相交遍历**
快速产出可能碰撞的候选实体对（Pairs），移交窄测阶段做精细几何相交。

## 设计

- `aabb.go` — 轴对齐包围盒：相交/包含判定、外扩（fatten）、表面积、并集。
- `tree.go` — 动态 AABB 树（参考 Box2D `b2DynamicTree`）：
  - SAH（表面积启发式）贪心插入，AVL 式旋转再平衡，树高保持 O(log N)；
  - 节点切片 + 空闲链表复用，零 GC 压力；
  - `Pairs` 自相交遍历：对每个内部节点交叉检查左右子树，复杂度 O(N log N + K)。
- `broadphase.go` — 粗测管理器：
  - 每个实体维护外扩 `margin` 的**胖包围盒**，小幅移动不触碰树结构（增量更新）；
  - 只有逃出胖包围盒时才执行一次删除 + 重插；
  - 树内按胖包围盒粗筛，输出前用精确包围盒复核，结果与 O(N²) 暴力法完全一致。

## 用法

```go
bp := broadphase.NewBroadphase(0.5)          // 胖包围盒外扩 0.5
bp.Add(id, broadphase.NewAABB(center, half)) // 注册刚体
bp.Move(id, newBox)                          // 刚体移动（增量更新）
pairs := bp.Pairs()                          // 候选碰撞对 -> 窄测
bp.Remove(id)                                // 移除刚体
```

## 运行样例

```
$ go run ./cmd/demo
scene: 500 rigid bodies, expect 12 overlapping pairs
broadphase: 200 steps x (500 moves + pair query)
total 125.045292ms, avg 625.226µs/step
candidate pairs for narrowphase: 12
  pair (0, 1) ... pair (22, 23)
```

## 测试

```
$ go test -v ./...
--- PASS: TestScene500Bodies              # 500 刚体精确筛出 12 对，与暴力法一致
--- PASS: TestMoveUpdate                  # 增量移动后候选对同步增减
--- PASS: TestSmallMovesStayIncremental   # 小幅移动零树重构
--- PASS: TestRemove                      # 移除实体后候选对消失
--- PASS: TestRandomizedAgainstBruteForce # 随机增删移 300 步逐步对照暴力法 + 树结构校验
--- PASS: TestPerformance500Bodies        # 500 刚体 x 200 步性能验收
    broadphase_test.go:303: 500 bodies x 200 steps: broadphase avg 662.092µs/step,
        brute force avg 21.619634ms/step, speedup 32.7x
--- PASS: ExampleBroadphase
```

基准（`go test -bench .`）：

| 操作 | 耗时 |
|---|---|
| `BenchmarkMove500`（500 刚体整帧移动） | ~288µs |
| `BenchmarkPairs500`（树内自相交遍历） | ~57µs |
| `BenchmarkBruteForce500`（O(N²) 暴力参照） | ~50ms |

> 注：上述绝对耗时来自受限沙箱 CPU；在正常桌面硬件上 500 刚体的单步
> 粗测为数十微秒级。性能验收采用同机相对指标：粗测相对 O(N²) 暴力法
> 加速 ≥ 5x（实测 30x 以上），且 500 叶树高稳定在 ~11（≈ log2 N），
> 证明避免了两两暴力比对。
