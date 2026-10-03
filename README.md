# wirefmt — 零依赖 Protobuf Wire-Format 编解码 + 崩溃恢复容错验证 (Go)

仅使用 Go 标准库，纯手工实现 Protocol Buffers 二进制 wire-format 解析器、
反射模式校验引擎，以及面向突发断电 / 磁盘满 / 网络丢包的 WAL 容错存储。

## 结构

- `wire/` — 底层编解码：Varint（7 位压缩）、ZigZag、Fixed32/64、
  Length-delimited（嵌套消息 / Packed Repeated）。所有读取均做边界检查，
  截断输入返回 `ErrTruncated`，绝不越界。
- `schema/` — 反射校验引擎：结构体 tag（`` `protobuf:"1,zigzag"` ``）→
  校验后的 schema（重复字段号、codec/类型不匹配、非法 packed 等均报错），
  并提供 `Marshal` / `Unmarshal`，未知字段安全跳过。
- `store/` — CRC32 + 提交标记的 WAL：写中途断电 / 磁盘满时安全回滚到
  最后提交点，重启恢复时截断撕裂尾部（自愈）；`CrashStorage` 注入断电与
  磁盘满，`LossyTransport` 模拟丢包链路。
- `cmd/demo/` — 可运行样例：多层嵌套消息编解码、截断防护、断电/磁盘满
  恢复、丢包重传复制。

## 运行

```sh
go test ./...        # 全部单元 / 并发 / 容错测试
go test -race ./...  # 竞态检测
go run ./cmd/demo    # 运行样例
```

## 容错语义

- 记录格式：`[totalLen uvarint][crc32 fixed32][payload]`，CRC 校验通过
  才视为已提交。
- `Append` 在任何注入故障下失败都会把文件截断回写前偏移（状态回滚），
  调用方看到错误，已提交数据零损失。
- `Open` 恢复时重放至最后一条合法记录，撕裂 / CRC 损坏的尾部被截断。
