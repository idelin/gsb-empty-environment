# jpegrotate

图片服务旋转照片时重新压缩会损失画质。给Go图片服务实现基线JPEG无损旋转包：熵解码到DCT系数，按90、180、270度和镜像对每个块做系数变换，正确处理4:2:0与4:4:4采样的块排列，不足整MCU的边缘按选项裁掉或拒绝；量化表相应转置后重新哈夫曼编码。解码旋转前后的图像，核对像素与先解码再旋转的结果一致。

## 用法

```go
var out bytes.Buffer
err := jpegrotate.TransformJPEG(&out, srcReader, jpegrotate.Rot90, jpegrotate.Options{Trim: true})
```

- 支持的变换：`Rot90`、`Rot180`、`Rot270`、`FlipH`、`FlipV`。
- 仅支持基线（顺序式、8-bit、Huffman）JPEG；渐进式会报错。
- 全程在量化DCT系数域变换，不重新量化，无代际损失。
- 宽高不是MCU整数倍时：`Options.Trim=false`（默认）返回错误；`Trim=true` 裁掉右/下边缘不足整MCU的部分再变换。
- 90/270度变换会相应转置量化表并交换采样因子；APPn/COM元数据、DHT表、重启间隔（DRI/RST）原样保留。

## 结构

- `jpegrotate.go` — 公共 API 与数据结构
- `parse.go` — 标记段解析（SOF/DQT/DHT/DRI/SOS）与熵数据提取
- `scan.go` — 位流读写与MCU级哈夫曼编解码
- `transform.go` — DCT系数块变换、块网格重排、MCU裁剪
- `encode.go` — 重新写出JPEG（量化表转置后重新哈夫曼编码）
- `jpegrotate_test.go` — 与"先解码再旋转"像素结果的一致性核对

测试：`go test ./...`
