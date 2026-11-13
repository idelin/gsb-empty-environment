# 状态列表压缩上限 (Status List Compression Limit)

`status_list` 的压缩字节可以非常小，但帧内**声明的解压长度**却可以填成一个
巨大的值（解压炸弹 / decompression bomb）。本包在**解压之前**先校验声明长度，
超过上限立即失败；解压完成后再强制要求实际长度与声明长度完全一致。小位图可以
正常解压并读取。

## 帧格式

在 Token Status List 的 ZLIB（RFC 1950，承载 DEFLATE/RFC 1951）压缩字节之前，
增加一个不可信的 8 字节长度前缀：

| 偏移 | 长度 | 含义 |
| --- | --- | --- |
| 0 | 8 | 大端 `uint64`，**声明的解压后字节数**（攻击者可控） |
| 8 | 其余 | ZLIB 压缩的位图字节流 |

## 解码器裁决顺序（`DecodeStatusList`）

1. 帧长度不足 8 字节 → `ErrShortFrame`。
2. 声明长度 `> MaxDeclaredSize`（1 MiB）→ **解压前**直接返回
   `ErrDeclaredTooLarge`，不分配大内存、不触碰压缩流。
3. ZLIB 流无法完整读取 → `ErrMalformed`（读取用 `io.LimitReader` 限制在
   `声明长度+1` 字节，内存始终有界）。
4. 实际解压长度 `!=` 声明长度 → `ErrLengthMismatch`。
5. 全部通过后返回位图；`BitAt` 按 LSB 优先读取 1-bit 状态。

## 关键 API

- `netsec.DecodeStatusList(frame []byte) ([]byte, error)`
- `netsec.BitAt(bitmap []byte, index int) (int, error)`
- 常量 `netsec.MaxDeclaredSize = 1 << 20`、`netsec.LengthPrefixSize = 8`
- 哨兵错误 `ErrShortFrame` / `ErrDeclaredTooLarge` / `ErrLengthMismatch` /
  `ErrMalformed`（用 `errors.Is` 判断）

## 声明长度超过上限的压缩输入

`testdata/malicious_oversize_declaration.bin`（21 字节）：

- 前 8 字节 `00 00 01 00 00 00 00 00` → 声明解压长度
  `1099511627776`（1 TiB），远超 1 MiB 上限。
- 后 13 字节 `78 9c 62 00 04 00 00 ff ff 00 01 00 01` 是合法 ZLIB 流，
  实际只解压出 **1 个字节**。
- base64：`AAABAAAAAAB4nGIABAAA//8AAQAB`

对照的合法小位图为 `testdata/valid_small.bin`（声明 2 字节，正常解压可读）。

## 验证组件（调用可导入的 Go 解码器）

`cmd/adjudicate` 导入本包并由 `DecodeStatusList` 做出裁决，退出码 0 接受、
1 拒绝：

```sh
# 沙箱内 git 不可用导致 VCS stamping 失败，需加 -buildvcs=false
export GOFLAGS=-buildvcs=false
go test ./...
go run ./cmd/adjudicate testdata/valid_small.bin                  # ACCEPT, exit 0
go run ./cmd/adjudicate testdata/malicious_oversize_declaration.bin # REJECT, exit 1
```

重新生成测试向量：`go run ./cmd/genvectors`（输出到 `testdata/`）。
