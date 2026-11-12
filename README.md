# 可微颜色管线

`taskpkg` 提供供 Go 合成宿主嵌入的节点式 RGBA 颜色管线。矩阵、幂传递函数和 over
合成均使用浮点数在线性光域计算；输入按连续 RGBA 分量传入，输出包含 RGBA 和对全部输入分量的
`4 × len(inputs)` 雅可比。

## 节点

- `InputNode`：选择一个带 alpha 的 RGBA 输入。
- `MatrixNode`：只对 RGB 应用给定 3×3 矩阵，alpha 原样传递。
- `PowerNode`：对 RGB 应用同一组分段幂函数，alpha 原样传递。
- `PremultiplyNode`：直线 RGBA 转预乘 RGBA。
- `UnpremultiplyNode`：预乘 RGBA 转直线 RGBA。
- `OverNode`：对已经预乘的 source/destination 执行 Porter-Duff over。

预乘 over 的 RGB 公式为：

```text
co = cs + (1 - as) cd
ao = as + (1 - as) ad
```

因此 `OverNode` 不会再次给预乘颜色乘 alpha。典型管线先在直线颜色上完成矩阵和传递函数，再调用
`PremultiplyNode`，最后进入一个或多个 `OverNode`。

## 分段幂与透明约定

`PowerSegment` 将 `[InputMin, InputMax]` 映射到 `[OutputMin, OutputMax]`，幂指数为 `Gamma`。
分段必须连续；位于拐点时选择右侧分段，因此返回右导数。

`UnpremultiplyNode` 在 alpha 小于等于零时返回全零 RGB 和 alpha=0，RGB 对所有输入分量的导数也为零；
alpha 自身仍按透传处理，所以 `d alpha_out / d alpha_in = 1`。

## 示例

```go
segments := []taskpkg.PowerSegment{
    {InputMin: 0, InputMax: 0.5, OutputMin: 0, OutputMax: 0.3, Gamma: 0.7},
    {InputMin: 0.5, InputMax: 1, OutputMin: 0.3, OutputMax: 1, Gamma: 2.5},
}

source := taskpkg.NewPremultiplyNode(
    taskpkg.NewMatrixNode(taskpkg.NewPowerNode(taskpkg.NewInputNode(0), segments), matrix),
)
destination := taskpkg.NewPremultiplyNode(taskpkg.NewInputNode(1))
root := taskpkg.NewOverNode(source, destination)

rgba, jacobian, err := taskpkg.Evaluate(root, inputs)
```

`jacobian[row][column]` 表示第 `row` 个输出 RGBA 分量对第 `column` 个展平输入分量的导数。

## 验证

`pipeline_test.go` 使用中心有限差分检查非拐点节点和多图层链式法则，并单独断言透明边界的解析值/导数、
拐点右导数以及 over 不重复乘 alpha。
