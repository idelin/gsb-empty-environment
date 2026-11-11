# rtmpchunk

推流网关要接收RTMP推流。给Go推流网关实现嵌入连接处理的chunk流解复用包：处理四种chunk头格式、多个csid交错、流中途的Set Chunk Size，以及扩展时间戳在类型3头中的重复规则，按消息长度重组完整消息；时间戳增量累加要处理32位回绕。用分块交错的构造流测试重组结果，并枚举TCP读取的切分位置。

## 实现

- `rtmpchunk.go`：RTMP chunk 流解复用器。`NewDemuxer(r)` 包装连接的
  `io.Reader`，`ReadMessage()` 按消息长度重组并返回完整 `Message`。
  - 四种 chunk 头（Type 0/1/2/3）与三种 Basic Header 长度
    （csid 2..63、64..319、320..65599）。
  - 每个 csid 独立维护状态，天然支持多 csid 在 chunk 粒度交错。
  - 自动处理 Set Chunk Size（协议控制消息类型 1），新尺寸仅作用于
    后续 chunk；控制消息本身仍按旧尺寸切分。
  - 扩展时间戳：Type 3 头在 `hadExtTS` 为真时重复读取 4 字节扩展字段；
    Type 3 新消息在 Type 1/2 头之后重新应用一次 delta，Type 0 头之后
    复用绝对时间戳。
  - 时间戳以 `uint32` 累加，无符号溢出即实现 32 位回绕。
  - 所有读取走 `io.ReadFull`，对 TCP 的任意字节切分不敏感。

## 测试

- `chunkencoder_test.go`：测试用的有状态 chunk 编码器，按字段变化选择
  头格式，并复现扩展时间戳重复规则。
- `rtmpchunk_test.go`：四种头格式与重组、多 csid 交错、流中途
  Set Chunk Size、协议错误与截断处理。
- `rtmpchunk_ext_test.go`：扩展时间戳原始字节布局、delta 重新应用、
  32 位时间戳回绕，以及把每次 TCP 读取限制为 1..N 字节枚举所有切分位置。

```sh
go test ./...
```
