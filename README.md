# bvh — AABB 动态 BVH 树（Go 标准库）

基于轴对齐包围盒（AABB）的层次包围盒（BVH）树，适用于三维/二维空间物体的
快速射线求交。仅依赖 Go 标准库。

## 特性

- **SAH 最优二分构建**：`Build` 对每个轴按质心排序后扫描全部二分点，
  以表面积启发式（Surface Area Heuristic）选取代价最小的划分；
  当二分不优于直接成叶时停止（叶子容量 2）。
- **动态插入/删除**：`Insert` 沿"包围盒表面积增量最小"路径下降到叶子后
  挂接新节点；`Remove` 移除图元并收缩空叶子。两者均自底向上 refit 包围盒，
  无需整树重建。
- **射线求交（Ray Cast）**：`Raycast` 用 slab 法做 AABB 相交测试，
  子节点按进入距离近者优先遍历，进入距离超过当前最近命中的子树被整体跳过，
  返回精确最近命中图元。
- **二维/三维通用**：图元通过 `Primitive` 接口抽象（`Bounds`/`Centroid`/
  `Intersect`）。内置三维 `Triangle`（Möller–Trumbore）；二维场景将 Z 分量
  置 0 即可（退化包围盒的 SAH 代价自动回退为周长），测试中以二维圆盘验证。

## 项目结构

```
vec.go            三维向量运算
aabb.go           AABB：并集/表面积/slab 法射线测试
ray.go            射线定义
triangle.go       三角形图元 + Möller–Trumbore 求交
bvh.go            BVH 树：SAH 构建、Insert/Remove、Raycast
bvh_test.go       验收测试与单元测试
scene_test.go     测试场景/射线生成器
cmd/bvhdemo/      运行样例
```

## 使用示例

```go
tree := bvh.New()
tree.Build(prims)          // prims: []bvh.Primitive（如 bvh.Triangle）

tree.Insert(p)             // 动态插入
tree.Remove(p)             // 动态删除

hit, stats := tree.Raycast(ray)   // hit.Prim 为最近命中图元, hit.T 为距离
```

## 运行

```sh
go test -v ./...     # 测试
go run ./cmd/bvhdemo # 样例
```

## 验收结果（go test -v）

场景：20 个星团共 **1000 个随机三角形**散布于 120³ 空间；发射 **100 条**
空间射线（2/3 瞄准星团密集区，1/3 随机方向）。

```
=== RUN   TestRaycastAcceptance
--- PASS: TestRaycastAcceptance
    构建: 1000 图元, 603 叶子, 耗时 57.8ms
    射线: 100 条, 命中 24 条, 总耗时 8.4ms (平均 84µs/条)
    剪枝: 平均访问叶子 1.6/603 (跳过 99.73%), 平均精确测试 3.0/1000 图元
=== RUN   TestDynamicInsertRemove   --- PASS   # 插入 750 / 删除 600 后与暴力法逐一比对
=== RUN   TestBoundsContainment     --- PASS   # 动态扰动后包围盒包含性不变式
=== RUN   Test2DScene               --- PASS   # 二维圆盘场景（Z=0 平面）
=== RUN   TestTriangleIntersect     --- PASS
=== RUN   TestAABBHit               --- PASS
```

验收边界核对：

| 指标 | 要求 | 实测 |
|---|---|---|
| 场景规模 | 1000 三角形 | 1000 三角形 / 603 叶子 |
| 射线数量 | 100 条 | 100 条 |
| 叶子剪枝率 | ≥ 95% | **99.73%**（平均仅访问 1.6/603 叶子） |
| 返回延迟 | 毫秒级 | 100 条共 **8.4ms**（平均 84µs/条） |
| 命中正确性 | 精确最近物体 | 与暴力法逐射线比对一致 |

## 样例输出（go run ./cmd/bvhdemo）

```
[构建] 1000 个三角形, SAH 建树耗时 191ms, 叶子数 603
[求交] 100 条射线, 命中 24 条, 总耗时 422µs (平均 4.2µs/条)
[剪枝] 平均访问叶子 1.6/603 (跳过 99.73%), 平均精确三角形测试 3.0/1000 次
[样例] 最近命中: t=144.5406, 三角形 V0=[17.376 18.663 42.722]
[动态] 删除 200 + 插入 200 后, 图元数 1000, 叶子数 681
[动态] 更新后沿 -Z 射线未命中
```
