# 扫描文档采集得到的OCR词框含重复识别和轻微旋转，逐行聚类会把表格两列粘在一起

扫描文档采集得到的OCR词框含重复识别和轻微旋转，逐行聚类会把表格两列粘在一起。实现供Go文档采集宿主调用的行结构包，输入四边形词框、文字和置信度，先按给定页面旋转归一，再以基线距离和水平重叠约束建立候选行；同位置重复词选择最高置信并保留候选证据。至多24词，求总代价最小的有序行划分，同一行按x中心排序，相邻词基线距离不得超过输入阈值且水平重叠不超较短词宽的1/4；总代价为行数乘输入罚值加同行基线距离平方和，平局按词ID分组列表排序。用小集合全划分核对双栏、上标及重复框，不调用外部OCR。

## 使用方式

包名为 `taskpkg`，入口是 `BuildLines`（`Build` 是简写别名）：

```go
res, err := taskpkg.BuildLines(words, taskpkg.Options{
    PageRotationDegrees: 0.5,
    BaselineThreshold:   4,
    LinePenalty:         40,
})
```

`words` 是 `[]taskpkg.Word`，每个词包含 `ID`、四边形 `Quad`（四个
`Point`）、`Text` 和 `Confidence`。返回的 `Result.Lines` 已按从上到下、
从左到右排序，行内词按 x 中心排序；`Result.Duplicates` 保存同位置重复
识别的全部候选和最高置信选中项。

## 规则摘要

- 先按 `PageRotationDegrees` 将四边形旋转回水平方向。
- 同位置候选按较小框面积覆盖率聚类，选置信度最高者，证据不丢失。
- 相邻词要求基线距离不超过 `BaselineThreshold`，水平重叠不超过较短词宽
  的 `1/4`。默认还限制水平间隙不超过较短词宽的 `1/4`，用于切断双栏；
  可用 `MaxGapRatio < 0` 关闭。
- 去重后超过 24 个词返回 `ErrTooManyWords`。
- 目标函数为 `行数*LinePenalty + Σ相邻基线距离²`，使用带分支定界的精确
  集合划分搜索；代价相同时按“各行词 ID 列表”的字典序确定，行按最左词
  的 x 顺序组成该列表。
- 测试中的 `TestRandomSmallSetsMatchFullEnumeration` 用受限增长串枚举全部
  集合划分，与求解器结果逐例核对。整个过程不调用任何外部 OCR。
