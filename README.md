# yuvconv

视频截图组件拿到的 YUV 帧来源各异，常把有限范围当全范围、把 BT.709 当
BT.601 处理。`yuvconv` 把这些差异显式化：调用方用 `Params` 描述帧的矩阵、
范围和色度采样位置，包负责正确转换到 8 位 sRGB（`image.RGBA`）。

## 支持范围

- **格式**：平面 I420、半平面 NV12、10 位 P010（分量位于 uint16 高 10 位）。
- **矩阵**：BT.601、BT.709、BT.2020（非恒定亮度），由标准 Kr/Kb 推导系数。
- **范围**：有限范围（TV，16..235/16..240，按位深等比缩放）与全范围（PC）。
- **色度位置**：`ChromaLeft`（MPEG-1/JPEG 共点，直接取块采样）与
  `ChromaCenter`（MPEG-2 中心，双线性插值，边界钳制）。
- **奇数宽高**：色度平面按 `ceil(w/2) × ceil(h/2)` 处理，末行末列不会越界。
- **钳制**：转换结果四舍五入后钳制到 0..255，非法参数返回 `ErrInvalidParams`。

## 用法

```go
img, err := yuvconv.NV12(yuvconv.NV12Frame{Y: yPlane, C: cPlane}, yuvconv.Params{
    Width:  1920,
    Height: 1080,
    Matrix: yuvconv.MatrixBT709,
    Range:  yuvconv.RangeLimited,
    Chroma: yuvconv.ChromaCenter,
})
```

带对齐填充的平面通过 `YStride` / `CStride`（P010 以 uint16 为单位）传入；
留空表示紧密排列。

## 测试

- `TestSMPTEBarsKnownValues`：用 100% SMPTE 标准色条的已知 BT.601 有限范围
  YUV 数值核对三种格式，最大误差 ≤3 个码值，I420/NV12 逐像素一致，P010
  仅差 10 位量化（≤1）。
- `TestRoundTripAllMatricesRanges`：三种矩阵 × 两种范围的编码-解码往返。
- `TestOddDimensionsNoPanic` / `TestOddEdgeChroma`：1x1 起的奇数尺寸、
  填充 stride 与边缘色度语义。
- `TestChromaLocation`、`TestClamping`、`TestValidation`：插值权重、
  越界钳制和非法输入校验。
