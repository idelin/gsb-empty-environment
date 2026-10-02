# stbvh — 四维时空（3D + Time）BVH 索引器

面向移动物体时空轨迹的索引结构：三维坐标 + 一维时间轴构成 4D AABB，
用 BVH（分桶 SAH 构建）加速"时间段 + 空间区域"的轨迹段检索。仅依赖 Go 标准库。

## 结构

- `aabb.go` — `Box4` 四维轴对齐包围盒（X/Y/Z/T，T 为 Unix 秒）
- `trajectory.go` — `Trajectory`/`Segment`，命中按查询时间窗线性裁剪
- `bvh.go` — `BVH` 构建（分桶 SAH）与 4D 剪枝查询
- `cmd/demo` — 无人机巡航查询运行样例

## 用法

```go
bvh := stbvh.BuildTrajectories(trajs...)
hits := bvh.Query(stbvh.Box4{
    Min: [4]float64{0, 0, 100, t1},   // 核心区 x,y∈[0,1000]，高度≥100m
    Max: [4]float64{1000, 1000, math.Inf(1), t2}, // 时间段 [t1, t2]
})
// 每个 Hit 含原线段及裁剪到时间窗后的精确片段 Clip0/Clip1
```

## 运行

```sh
go test ./... -v        # 测试（含暴力对拍、验收场景、10 万段性能测试）
go test -bench=. .      # 基准
go run ./cmd/demo       # 无人机样例
```

## 性能

99,500 线段上单次区域查询约 0.2–1.4 ms（Apple M1 Pro），毫秒级返回。
