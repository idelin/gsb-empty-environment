# bvhtrace

供离线渲染器着色阶段调用的 Go BVH 光线求交加速包。核心目标是替代逐三角形暴力求交，并消除光线从三角形共享边漏过的问题。

## 特性

- **SAH 分箱构建**：在每个内部节点上对三个轴做 12 桶表面面积启发式（Surface-Area Heuristic）分箱，选择成本最低的分割；当叶节点成本更优时直接成叶，构建为期望 O(n log n)。
- **近远有序遍历与提前终止**：内部节点的两个子节点按入点距离排序，远子节点先压栈、近子节点后压栈，从而由近到远访问；最近交点持续收窄光线的 `Tmax`，被遮挡的子树直接跳过。
- **防水三角求交**：实现 Woop/Benthin/Wald《Watertight Ray/Triangle Intersection》(2013)。每条光线只做一次单精度剪切变换，共享同一条边的三角形以位级相同的算术计算该边；精确为零的边函数通过反对称边方向谓词只归属于相邻两三角形之一，因此命中共享边时既不漏过也不重复命中。支持双面（不剔除背面）。
- **阴影 any-hit 查询**：`Occluded` 只判断是否被遮挡，找到第一个阻挡体立即返回，不搜索最近交点。
- **暴力对照**：`BruteForceTraverse` / `BruteForceOccluded` 复用同一套防水求交核，逐条三角形扫描，用于验证加速结构返回的最近三角形编号与距离。

## API

```go
tris := []bvhtrace.Triangle{ /* A, B, C 三个顶点 */ }
bvh := bvhtrace.Build(tris)                 // 线程安全：构建后只读

ray := bvhtrace.NewRay(origin, dir, tmin, tmax) // tmax<=0 表示无限远

hit, ok := bvh.Traverse(ray)               // 最近交点
// hit.Primitive 是三角形编号；hit.WA/WB/WC 为 A/B/C 的重心权重

if bvh.Occluded(shadowRay) { /* 在阴影中 */ } // any-hit，提前终止
```

包内类型与文件：

- `vec.go` / `ray.go`：`Vec3` 向量与 `Ray`（预计算 `invDir` 供无分支 slab 测试）。
- `bbox.go`：`AABB` 及稳健的光线/包围盒 slab 求交（零方向分量不产生 NaN 误判）。
- `triangle.go`：防水求交核 `intersectTriangle`，返回距离与重心权重。
- `bvh.go`：SAH 分箱构建、有序栈式遍历、`Traverse`/`Occluded`/暴力对照。
- `hit.go` / `scene.go`：`Hit` 结果，以及程序化网格（`GroundGrid`、`RandomScene`、`RandomRays`）。

## 验证与基准

测试覆盖：基础命中与重心插值、光线区间、共享边防水（精确边 + 双侧抖动 + 2 万条密集扫描零漏射）、BVH 与暴力逐光线最近三角形一致性、阴影一致性、空/退化场景，以及 8 协程并发只读查询。另含与教科书 Möller–Trumbore 的 75 万次独立交叉验证，内部命中判定零分歧。

```sh
go test ./...            # 正确性 + 防水 + 一致性
go test -race ./...      # 并发安全
go test -bench . ./...   # 构建/遍历/阴影/暴力 基准
```

命令行演示会构建场景、计时构建与查询，并逐条光线比对最近交点的三角形编号：

```sh
go run -buildvcs=false ./cmd/bvhtrace-bench -tris 20000 -rays 5000
```

Apple M1 Pro 上 2 万三角形 / 5 千条随机光线的代表性结果：

- SAH BVH 构建约 11 ms。
- 最近交点：BVH 约 201 µs/ray，暴力约 607 µs/ray，约 **3.0x** 加速，三角形编号与距离零分歧。
- 阴影 any-hit：约 2.3 µs/ray（找到首个阻挡体即返回）。
- 10 万条近边/共边光线：**0 漏射**。

注：演示光线为低相干性的全随机方向，加速比偏保守；着色阶段的主光线/阴影束相干性更高，实际收益通常更大。
