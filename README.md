# Go MVCC 键值存储示例

这是一个只依赖 Go 标准库的进程内 MVCC 键值存储，支持：

- 多键读取固定在同一个快照版本，不能读到“A 旧、B 新”的混合状态。
- 写入批次共享同一个新版本，失败时不发布任何键。
- 数据源通过 `SourceFunc` 注入；批次和每条记录必须显式携带同一个 revision，避免跨版本拼装。
- 时钟和故障通过 `ClockFunc`、`FailFunc` 注入，便于测试 TTL 和失败路径。
- 快照存活期间保留最老活跃快照需要的历史版本；删除墓碑在旧快照仍可见。
- `SnapshotTTL > 0` 时，到点后的读返回 `ErrSnapshotExpired`；`0` 表示不过期，必须显式 `Close()`。
- 过期快照不会自动阻塞回收，但仍需显式 `Close()` 解除资源登记；也可以在 TTL 后立即关闭。
- 写/源加载后自动回收；也可以调用 `GC()` 取得 `GCStats`。
- 不依赖 Redis、Memcached 或其他远程缓存。

## 运行

```sh
go test ./... -race -count=1
go run ./cmd/mvcc-demo
```

## 本机验证结果

测试：

```text
?   mvcc/cmd/mvcc-demo [no test files]
ok  mvcc/mvcc 1.922s
```

演示：

```text
old snapshot (version 1): A="A-v1" B="B-v1" deleted=[] missing=[]
latest state (version 3): A="A-v2" B="B-v2-from-source" deleted=[] missing=[]
deleted B is still visible in old snapshot (version 1): A="A-v1" B="B-v1" deleted=[] missing=[]
ttl expired=true; read after expiry error=mvcc: snapshot has expired
gc while snapshot active: removed entries=0 watermark=4 active=1
gc after snapshot release: removed entries=1 watermark=5 active=0
```

## API 概览

- `New(Config{...})`：创建存储，可注入 `Source`、`Clock`、`Fail` 和 `SnapshotTTL`。
- `Write([]Record)`：原子写入或删除一批键。
- `Load([]string)`：通过注入数据源原子发布一批同 revision 记录。
- `Snapshot()`：创建当前版本快照。
- `Snapshot.Read([]string)` / `Store.Read([]string)`：多键同版本读取。
- `Snapshot.Close()`：关闭快照并解除版本固定。
- `GC()`：按最老活跃快照版本执行回收并返回统计。
- `Close()`：关闭存储，清空版本链和快照登记。
