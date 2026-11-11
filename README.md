# walrestore

移动端数据同步库从WAL恢复最近的已提交快照。写Go可迭代恢复包，处理跨块片段、批次序号与删除标记，CRC或长度错误只能在允许的撕裂尾部截断。消费者以不可变视图读取指定序号，测试损坏中段、取消恢复和重复打开不会发布部分批次。

## Layout

- `walrestore.go` — recovery entry points, committed `Store`, immutable `View`, sequence and tombstone handling.
- `reader.go` — fixed-block fragment format, streaming `Iterator`, CRC/length validation, torn-tail vs. mid-log corruption.
- `writer.go` — `Writer` that emits block-bounded fragments and the batch record codec.
- `view.go` — range-over-func iteration (`iter.Seq2`) over an immutable snapshot.

## Guarantees

- A batch is published only after its full, checksum-verified record is decoded; partial batches never surface.
- CRC or length errors in the final physical block truncate the torn tail (`ErrTruncated`, recoverable); the same errors in any earlier block are `ErrCorruption`.
- Batch sequence numbers must increase strictly by one; gaps or duplicates are corruption.
- Delete markers tombstone keys until a later put resurrects them.
- Views are pinned to a sequence number; returned slices are independent copies, so historical views stay immutable.
- `Recover`/`Open` honor `context.Context`; cancellation returns an error and no partial store.

## Usage

```go
store, err := walrestore.Open(ctx, "sync.wal")
if err != nil {
    log.Fatal(err) // ErrCorruption for mid-log damage
}

view, err := store.ViewAt(store.MaxSeq())
if err != nil {
    log.Fatal(err)
}
for key, value := range view.All() {
    // key/value are copies; mutating them cannot change the store
    _ = key
    _ = value
}
```

## Tests

```sh
go test -race -count=1 ./...
```

Coverage includes cross-block reassembly, mid-log CRC/length corruption,
torn-tail truncation, cancellation, repeated concurrent opens, tombstones and
immutable views.
