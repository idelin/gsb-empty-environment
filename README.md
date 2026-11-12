# 追加文件语义代理

审计卷只授权追加，普通写权限包装仍可能允许覆盖旧字节。写供Go服务调用的追加句柄包，写入偏移由服务端原子分配，多段写允许短写但只暴露已提交连续前缀；调用方不得自行移动位置。取消后未提交预留必须填成显式空洞而非交给其他请求复用，用故障文件测试并发追加及中断恢复。

## 用法

```go
log, err := netsec.Open("/var/lib/audit/events.log")
if err != nil {
    return err
}
defer log.Close()

// One append can be split into multiple segments. The server assigns the
// offset; the caller never supplies one.
rec, err := log.Append(ctx, []byte(`{"event":"login"}`), []byte("\n"))
if err != nil {
    return err
}
_ = rec.Offset

// Readers only see the contiguous committed prefix.
if err := log.WaitVisible(ctx, rec.End()); err != nil {
    return err
}
records, err := log.Snapshot()
```

Lower-level callers can reserve a fixed payload length, write it in several
segments, and then commit or cancel:

```go
r, err := log.Reserve(int64(len(header) + len(body)))
if err != nil {
    return err
}
if _, err := r.WriteSegments(header, body); err != nil {
    _ = r.Cancel()
    return err
}
return r.Commit()
```

## 保证

- **服务端分配偏移**：`Reserve` 在互斥分配器上一次性取得互不重叠的区间；`Append` 也走同一条路径。`Log` 不暴露底层 `Store`，也没有 `Seek`、`WriteAt` 或由调用方传入偏移的写方法。
- **短写可容忍**：底层 `WriteAt` 返回短写时，`Reservation.Write` 会推进自己的服务端游标并重试同一段余下字节；多段写按段顺序推进。
- **只暴露已提交连续前缀**：记录必须完整写入 payload、写入 data trailer 并通过 `Sync`，才会进入可见前缀。乱序完成的后继记录会等待前面的预留解决，`Snapshot` 不会跳过空洞。
- **取消即显式空洞**：`Cancel`、写故障、`Sync` 失败和关闭时未完成的预留都会把整个 payload 清零并写入 `KindHole` trailer。`allocEnd` 永不回退，后续请求只能从空洞之后追加。
- **中断恢复**：`Open`/`OpenStore` 扫描带 magic、长度、偏移和 CRC 的记录；发现撕裂的 header、缺失的 trailer 或不完整 payload 时，将未提交尾部重写为显式空洞，已提交前缀不被触碰。
- **故障文件测试**：`append_test.go` 中的 `memStore` 可注入短写、单次写故障和 `Sync` 故障，覆盖并发追加、取消留洞、提交失败及崩溃恢复。

## 记录格式

每条记录由 32 字节 header、payload 和 16 字节 trailer 组成。header 保存
magic、payload 长度、记录起始偏移和 header CRC；trailer 保存 magic、
`data`/`hole` 类型以及 payload CRC。空洞 payload 内容为零，因此既不会被
误读为业务数据，也不允许分配器把该区间交给其他请求。
