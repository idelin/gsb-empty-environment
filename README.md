# 视频运动估计在重复纹理上容易得到不稳定向量

视频运动估计在重复纹理上容易得到不稳定向量。写供Go分析管线调用的块匹配包，对每个固定尺寸亮度块，在指定整数位移窗内先按绝对差和求全部最优候选，再用相邻块向量差的L1惩罚选择一行的全局最优解；越界候选禁止，平局按向量列表字典序。返回每块数据误差与平滑代价，不能用逐块贪心替代。用小图全组合枚举核对平移、遮挡及重复条纹，不足整块的边缘按有效像素归一误差。

## 使用

```go
cfg := taskpkg.Config{
	BlockSize: 8,
	Window: taskpkg.Window{
		MinDX: -4, MaxDX: 4,
		MinDY: -4, MaxDY: 4,
	},
	Lambda: 1.5,
}

result, err := taskpkg.Match(
	taskpkg.Frame{Luma: refLuma, Width: width, Height: height},
	taskpkg.Frame{Luma: dstLuma, Width: width, Height: height},
	cfg,
)
```

`Match` 对参考帧按 `BlockSize` 切块，块网格覆盖整幅图，右/下边缘允许不足整块。每个候选整数位移必须让该块全部有效源像素落入目标帧，否则直接排除；边缘块以 `Width*Height` 个有效像素计算 SAD，并以该数量归一化 `DataError`。

## 算法约束

- 每块先求 SAD 最小值，并保留所有达到该最小值的候选向量，不保留 SAD 更差的候选。
- 每条块行独立做动态规划，最小化整行的相邻块向量 L1 惩罚之和，因此不是逐块贪心。
- 候选按 `(DX, DY)` 字典序排列；目标值相同时选择整条向量序列字典序最小者。`Lambda == 0` 时全部序列目标值相等，也按该规则取首候选。
- `BlockResult.DataError` 为 `SAD / ValidPixels`；`BlockResult.SmoothnessCost` 为 `Lambda * SmoothnessL1`，首块平滑代价为 0。
- `Result.DataCost`、`Result.SmoothnessCost`、`Result.TotalCost` 为逐块代价之和。

`go test ./...` 包含平移、遮挡、重复条纹的小图全组合枚举核对，以及边缘归一化、越界候选和行级全局最优测试。
