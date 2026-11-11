# tmplmatch

桌面 RPA 流程步骤调用的模板匹配包。截图中的按钮图标会因 DPI 缩放和抗锯齿
与模板像素不一致，精确比对失效；本包用归一化互相关（NCC）做鲁棒匹配。

## 算法

- **积分图（summed-area table）**：对灰度值和灰度平方各建一张积分图，
  任意窗口的均值、方差 O(1) 取得，用于 NCC 分母；互相关分子用滑动点积。
- **图像金字塔粗到精**：场景与模板同步做 2×2 块均值降采样，先在最粗层
  全量扫描，再把粗命中映射到上一层的小邻域逐层精修，避免全分辨率逐点扫描。
- **透明遮罩**：模板 alpha < 128 的像素不参与统计，NCC 只在不透明像素上
  计算（遮罩路径走直接求和，无遮罩路径走积分图快速通道）。
- **多缩放比例**：模板按每个 scale 双线性缩放后分别匹配，覆盖图标被放大/
  缩小的情况。
- **非极大值抑制**：跨尺度按实际框尺寸计算 IoU，贪心保留最强命中，
  返回所有超过阈值的去重位置。

NCC 采用零均值形式 `Σ(T-μT)(I-μI) / sqrt(Σ(T-μT)² · Σ(I-μI)²)`，
零方差平坦窗口判 0 分，避免 NaN。

## 用法

```go
import "example.org/tmplmatch"

matches, err := tmplmatch.Match(screenshot, buttonIcon, tmplmatch.Options{
    Threshold: 0.8,
    Scales:    []float64{0.75, 0.9, 1.0, 1.1, 1.25},
})
for _, m := range matches {
    // m.Rect 是截图中的按钮位置，m.Score 是 0..1 的 NCC 分数
}
```

需要分阶段耗计时用 `tmplmatch.MatchWithStats`；需要对照参考结果用
`tmplmatch.BruteForceMatch`（全分辨率暴力 NCC，同一打分函数与 NMS）。

## 与暴力 NCC 的对比

`cmd/tmplmatch-bench` 生成带多个缩放/抗锯齿按钮（含一个透明遮罩按钮）的
合成截图，同时跑金字塔匹配和全分辨率暴力 NCC。Apple M1 Pro 实测：

| 场景 | 模板 | 尺度数 | 金字塔耗时 | 暴力 NCC 耗时 | 加速比 | 结果 |
| --- | --- | --- | --- | --- | --- | --- |
| 640×480 | 32×32 | 5 | ~53ms | ~2.4s | ~45× | 4/4 命中完全一致 |
| 1920×1080 | 40×40 | 5 | ~519ms | ~46s | ~89× | 4/4 命中完全一致 |

两条路径位置误差 0px、分数误差 0；金字塔只改变求值的位置数量，不改变
打分公式。运行方式：

```sh
go run -buildvcs=false ./cmd/tmplmatch-bench
go test ./...
```

## 文件结构

- `gray.go`：灰度化、双线性缩放、积分图
- `ncc.go`：模板统计量、NCC 打分、积分图/暴力/遮罩扫描器、Top-K 堆
- `tmplmatch.go`：对外 API、金字塔编排、层级精修、NMS
- `brute.go`：全分辨率暴力 NCC 参考实现
- `cmd/tmplmatch-bench`：结果与耗时对比命令
