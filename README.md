# reassemble

乱序分片重组库（仅 Go 标准库）。按 **消息 ID + 总长度 + 片偏移** 重组；
重叠/重复内容必须逐字节一致，否则报冲突；支持每消息与全局字节额度、
过期回收和完成去重。

## 组成

- `Reassembler`（reassemble.go）：分片重组核心。
  - 乱序、重叠、重复分片任意到达；重叠字节不一致返回 `ErrConflict`，
    绝不“任选一个”。
  - 同一 ID 的总长度不一致返回 `ErrTotalLenMismatch`。
  - 每消息额度 `MaxMessageSize` 与全局额度 `MaxBufferedBytes` 都在
    **分配缓冲区之前**检查；完成或过期即释放额度。
  - `TTL` 过期后旧缓冲区被丢弃，迟到分片从新消息重新开始，
    不借用旧缓冲区；`Expire()` 可主动清扫。
  - 完成后 ID 在去重窗口（`CompletionTTL`）内被记住，迟到重复分片
    返回 `(nil, true, nil)`，不再分配新消息。
- `Parser`（parser.go）：把任意分块的字节流解析成帧。
  - 数据不足（部分头/部分负载）静默缓冲，不是错误；
    坏 magic/版本/校验和/超长负载返回 `*BadDataError`，
    用 `IsBadData` 区分“缺数据”与“坏数据”。
  - 负载长度在分配前对照 `MaxPayload` 检查。
- `Handler`：Parser + Reassembler，字节进、完整消息出。

## 帧格式（31 字节定长头 + 负载）

```
[0:2]   magic 'F','G'        [15:19] 片偏移 uint32 BE
[2]     version = 1          [19:23] 负载长度 uint32 BE
[3:11]  消息 ID uint64 BE    [23:27] 负载 CRC32-IEEE
[11:15] 总长度 uint32 BE     [27:31] 头部 CRC32-IEEE
```

## 运行

```
go test ./...     # 测试
go run ./cmd/demo # 样例
```
