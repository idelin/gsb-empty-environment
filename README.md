# 图像尺寸声明像素预算

头像服务在解码前先看图像头，攻击者声明很大的宽高却只给很短的数据，
或用 APNG 帧数把总像素乘上去。`pixelbudget` 是可直接嵌入 Go 上传服务
的像素预算包：只解析 PNG（含 APNG）与 JPEG 头部，用溢出安全的乘法
计算单帧与全部帧像素，超预算时不调用解码分配回调；解码后的实际尺寸
与头部声明不一致也拒绝。

## 安全语义

- **只看头部，不碰像素**：PNG 在第一个 `IDAT` 前停下，JPEG 在 `SOS`
  扫描数据前停下，预算通过前不做任何像素缓冲分配。
- **溢出安全乘法**：所有乘法在 `uint64` 域内用 `a > MaxInt64/b` 预判，
  乘积超过 `math.MaxInt64` 直接按溢出拒绝，不存在整数回绕。
- **APNG 帧数计入总量**：读取 `acTL` 的 `num_frames`，
  `总像素 = 宽 × 高 × 帧数`，多帧放大攻击与单帧超限分别用
  `ErrSingleFrame` / `ErrTotalPixels` 标识。
- **超预算不回调**：只有全部预算检查通过后才调用 `OnAlloc` 分配回调，
  也只有此时才会触发底层解码。
- **声明/实际尺寸核对**：解码后比对 `img.Bounds()` 与头部宽高，
  不一致返回 `ErrSizeMismatch`。

## 用法

HTTP 请求体这类不可 Seek 的流用 `DecodeReader`：

```go
import "example.org/netsec/q0056/pixelbudget"

img, hdr, err := pixelbudget.DecodeReader(r.Body, pixelbudget.Options{
    Budget: pixelbudget.Limits{
        MaxFramePixels: 8_000_000, // 单帧上限
        MaxTotalPixels: 32_000_000, // APNG 全部帧总量上限
    },
    OnAlloc: func(h pixelbudget.Header) error {
        // 只有通过预算后才会执行：可在此预分配或记账。
        return reserveMemory(h.TotalPixels)
    },
})
if errors.Is(err, pixelbudget.ErrSingleFrame) || errors.Is(err, pixelbudget.ErrTotalPixels) {
    // 拒绝上传
}
```

可 Seek 的输入（`*bytes.Reader`、`*os.File`）用 `Decode`，无需缓冲整图；
只想做头部预检时用 `Inspect` + `Header.Check`。

## 验证

`go test ./...` 覆盖：65535×65535 的 PNG/JPEG 大声明短数据、乘法溢出、
APNG 多帧总量超限、解码尺寸与声明不符、普通小图全链路，以及截断/畸形
头部等防御分支（`-race` 下通过）。
