# seq — 号段式唯一序号库

通过**持久化预留号段**把刷盘频率从「每序号一次」降为「每号段一次」：
向存储原子地预留一段连续区间 `[end, end+step)`，之后纯内存发号，号段耗尽再预留下一段。

## 保证

- **刷盘失败不发号**：号段未成功持久化前，`Next` 只返回错误，绝不从未预留的区间发号。
- **重启可跳号、绝不复用**：持久化的只有高水位 `SegmentEnd`，它单调递增。
  重启后从最新高水位继续预留，崩溃号段的未用尾部被跳过（产生空洞），
  但任何已返回的序号永远不会再次返回。
- **跨进程协调只靠抽象 CAS**：所有并发协调都收敛到 `Store.CompareAndSwap`，
  任何提供原子比较更新的后端（文件、数据库、etcd…）都可接入。

## 接口

```go
type Store interface {
    Load() (State, error)                       // State{Version, SegmentEnd}
    CompareAndSwap(expect, next State) (bool, error) // 原子比较并持久化
}
```

- `MemStore`：内存实现，用于测试与单进程场景。
- `FileStore`：JSON 文件 + flock 互斥 + 临时文件 fsync 后原子 rename，可跨进程。

```go
store := seq.NewFileStore("/var/lib/myapp/seq.state")
alloc, _ := seq.NewAllocator(store, 1000) // 每次预留 1000 个号
id, err := alloc.Next()
```

## 故障枚举（`seq/fault` 故障注入存储）

`fault.Store` 包装任意 `Store`，在第 N 次 `CompareAndSwap` 注入故障，
测试据此枚举全部崩溃/失败场景：

| 场景 | 注入 | 存储落盘 | 行为 |
|---|---|---|---|
| 刷盘失败 | `FailBeforeFlush` | 否 | `Next` 返回错误，不发号；恢复后从原位置继续 |
| 暧昧刷盘 | `FailAfterFlush` | 是（调用方看到错误） | 该号段整段弃用跳过，重试预留新段 |
| 刷盘前崩溃 | `CrashBeforeFlush` | 否 | 未发任何号；重启后从原位置开始，无复用 |
| 刷盘后崩溃（未用） | `CrashAfterFlush` | 是 | 孤儿号段整段跳过 |
| 刷盘后崩溃（已用部分） | `CrashAfterFlush`（中途） | 是 | 已发号永不复用，未用尾部跳过 |
| 号段耗尽 | — | — | 自动 CAS 预留下一段；step=3 发 10 个号恰好刷盘 4 次 |
| 并发/跨进程竞争 | — | — | CAS 失败方重载重试；两个真实进程各发 300 号零重叠 |

另有一组随机化测试：50 轮「随机步长 + 随机崩溃点」的崩溃/重启循环，
断言全程所有已返回序号全局唯一、且重启后序号严格大于崩溃前最大值。

## 测试

```sh
go test ./...        # 含真实子进程跨进程竞争用例
go test -race ./...
```
