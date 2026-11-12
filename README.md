# 图像浏览器处理高动态范围帧时，需要局部压缩亮度而不反转边缘

图像浏览器处理高动态范围帧时，需要局部压缩亮度而不反转边缘。实现供Go显示管线调用的色调映射包，先对正亮度取对数，以空间距离和对数亮度差的双边权重估计基底，再按给定压缩比例映射基底并保留细节；RGB按亮度比例缩放，零亮度输出零。核半径、两个sigma及输出峰值均由输入给定。用朴素双重循环作为参考核对分块halo、常量帧及极亮单点，确保无NaN和边缘接缝。

## 用法

图像以交错 RGB 浮点切片表示，亮度采用 Rec.709 权重。`Peak` 是输出最亮通道的目标值；`Compression` 为 1 时基底不变，小于 1 时压缩大尺度亮度。

```go
cfg := taskpkg.Config{
    Radius:       3,
    SigmaSpatial: 1.5,
    SigmaRange:   0.25,
    Compression:  0.55,
    Peak:         1,
}

out := taskpkg.ToneMapRGB(frame.Pix, frame.Width, frame.Height, cfg)

// 同样的数学结果，用于分块实现的无接缝核对。
reference := taskpkg.ToneMapNaive(frame.Pix, frame.Width, frame.Height, cfg)
tiled := taskpkg.ToneMapTiled(frame.Pix, frame.Width, frame.Height, 32, cfg)
```

`Config` 还接受 `SigmaS`/`SigmaSpace`、`SigmaR`/`SigmaRange`、`CompressionFactor` 等字段名别名。`ToneMap` 泛型入口同时支持 `[]float32`、`[]float64`、`Image`、`Frame` 和按像素排列的 RGB 切片。
