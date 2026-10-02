# bvh — 动态 AABB BVH 树（Go 标准库）

三维/二维空间物体的层次包围盒（BVH）树，仅依赖 Go 标准库。
二维场景直接使用 `Z = 0` 的 `Vec3` 即可。

## 特性

- **SAH 最优二分构建**：`Build` 沿质心包围盒最长轴分 16 桶，枚举分割面取表面积启发式（SAH）代价最小者，自顶向下构建。
- **动态插入 / 删除**：`Insert` 按 SAH 代价（包围盒表面积增量）选择最佳兄弟节点分裂；`Remove` 摘除叶子并上提兄弟节点；两者均自底向上重拟合包围盒，无需整树重建。
- **射线查询与快速剪枝**：`RayCast` 用 slab 法做 AABB-射线相交测试，近者优先进栈遍历，并用当前最近命中距离 `t` 剔除更远子树；`RayCastWithStats` 返回遍历统计用于剪枝率分析。

## 结构

| 文件 | 内容 |
|---|---|
| `vec.go` | `Vec3`、`Ray` |
| `aabb.go` | `AABB`：并集、表面积、slab 射线测试 |
| `triangle.go` | `Triangle`：Möller–Trumbore 求交，实现 `Object` 接口 |
| `bvh.go` | `BVH`：SAH 构建、动态增删、射线遍历 |
| `bvh_test.go` | 验收测试与基准 |
| `cmd/demo/main.go` | 运行样例 |

自定义物体只需实现 `Object` 接口（`Bounds() AABB` 与 `Intersect(Ray) (float64, bool)`）。

## 运行

```sh
go test -v ./...   # 测试
go run ./cmd/demo  # 样例
```

## 验收结果（1000 三角形 + 100 射线）

- 正确性：BVH 最近命中与暴力法逐一比对，物体与距离完全一致。
- 剪枝率：平均每条射线仅访问 ~0.8/1000 个叶子，**跳过 99.9%+**（要求 ≥95%）。
- 性能：SAH 构建 ~8ms；100 条射线总计 ~3ms（**~0.03ms/条**）。
