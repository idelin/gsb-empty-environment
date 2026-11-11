# h264bs

媒体网关要在Annex B与AVCC两种H.264码流之间转换。给Go媒体网关实现码流转换包：识别3字节和4字节起始码，处理防竞争字节；从SPS用指数哥伦布码解析宽高、裁剪、帧率以及High profile才有的可选字段，SPS或PPS变化时更新avcC配置；长度前缀宽度支持1、2、4字节。用手工构造的SPS（含High profile与裁剪）往返转换，核对字节一致和解析出的分辨率。

## 功能

- `SplitAnnexB` / `PackAnnexB`：识别 3 字节（`00 00 01`）与 4 字节
  （`00 00 00 01`）起始码，自动丢弃 NAL 间的零填充；封装统一输出 4 字节起始码。
- `SplitAVCC` / `PackAVCC`：长度前缀宽度支持 1、2、4 字节，大端编码，
  超容量/截断/空 NAL 均返回错误。
- `EscapeRBSP` / `UnescapeRBSP`：EBSP 防竞争字节（`00 00 03`）双向转换。
- `ParseSPS`：指数哥伦布（ue/se）解析 SPS，覆盖 profile/level、High profile
  扩展（chroma_format_idc、位深、缩放矩阵）、POC、帧裁剪、VUI 时序/HRD，
  并推导可见分辨率（按 4:2:0/4:2:2/4:4:4 色度采样单位）与帧率
  `timeScale / (2 * numUnitsInTick)`。
- `BuildDecoderConfig` / `ParseDecoderConfig`：构建与解析
  AVCDecoderConfigurationRecord（avcC），High profile 自动追加扩展字节。
- `Converter`：有状态、带互斥锁；SPS/PPS 按参数集 id 缓存，内容变化时
  重建 avcC，转换接口返回配置是否变更。

## 用法

```go
conv, _ := h264bs.NewConverter(4) // AVCC 长度前缀 4 字节

// Annex B -> AVCC，SPS/PPS 变化时内部 avcC 自动更新
avcc, changed, err := conv.ConvertAnnexB(annexBStream)
avccConfig := conv.Config() // 可写入 MP4 avcC box

// AVCC -> Annex B
annexB, _, err := conv.ConvertAVCC(avcc)

// 直接解析 SPS 拿分辨率/帧率
sps, err := h264bs.ParseSPS(spsNAL)
fmt.Println(sps.Width, sps.Height, sps.FrameRate)
```

## 测试

```sh
go test -race -cover ./...
```

测试用包内比特写入器手工构造 High profile SPS（含帧裁剪、VUI 时序、
缩放矩阵、HRD、4:4:4 等），验证转义往返字节一致、Annex B/AVCC 往返
在 1/2/4 字节前缀下 NAL 载荷逐字节一致，以及裁剪后分辨率与帧率正确。
