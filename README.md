# noncelease

多个加密写入器共享同一把密钥时，nonce（计数器）绝不能重复。`noncelease`
是一个 Go 分配包，离线为各写入器发放互不相交的计数器段（租约），并把
“保留上界先落盘、后返回区间”作为核心不变量。

## 保证

- **先持久化后返回**：每次 `Allocate` 都把新的高水位以 CRC32 校验帧写进
  append-only WAL 并 `fsync`，成功后才把区间返回给写入器；崩溃不会让同一
  计数器被再次发放。
- **写盘撕裂可恢复**：重放时遇到短帧、坏魔数、CRC 不符或尾部垃圾，日志会
  截断到最后一个完整帧；写入失败即关闭存储，调用方不会把未知结果当成功。
- **租约跨重启失效**：租约只存在内存中，重启后全部失效（fencing token
  来自 `crypto/rand`），而持久化的高水位仍然保留，计数器不会回退重发。
- **TTL 回收不产生重复**：仅当某密钥版本所有在跟踪租约全部到期时，高水位
  才回零；永久租约（`ttl == 0`）或任一存活租约都会阻止回收。
- **密钥轮换**：`RotateKey` 封存旧版本并开启新版本，计数器从零重新计数；
  nonce 的唯一性按 `(keyID, version)` 界定，旧版本不再发放新区间。
- **全有或全无**：剩余计数器不足以满足整段请求时返回
  `ErrCounterExhausted`，不消耗任何计数器。

## 用法

```go
a, err := noncelease.Open("/var/lib/myapp/nonce.wal")
if err != nil { /* ... */ }
defer a.Close()

if _, err := a.CreateKey("master-key"); err != nil { /* ... */ }

lease, err := a.Allocate("master-key", 1_000_000, time.Hour)
if err != nil { /* ErrCounterExhausted 等 */ }

if err := a.Valid(lease); err != nil { /* 重启失效 / TTL 到期 / 已轮换 */ }

for c := lease.Interval.Start; c < lease.Interval.End; c++ {
    nonce := noncelease.EncodeCounter(c) // 8 字节大端
    // 用 nonce 加密；区间互不相交，故 nonce 不重复
}

// 轮换后新版本从 0 开始，旧版本自动封存
newVersion, _ := a.RotateKey("master-key")
```

`CreateKey(key, limit...)` 可传入独占的计数器上界（0 表示完整 uint64
空间），便于在小空间内测试耗尽行为。

## 测试

```sh
go test -race -count=1 ./...
```

测试用可注入故障的内存磁盘模拟掉电，覆盖：

- 多切点撕裂写、整帧字节损坏、整笔写丢失后的重放与重试；
- 两个写入器在 `-race` 下争抢最后一段，恰有一个成功、另一个收到耗尽错误；
- 计数器走到上限、轮换后重启仍保持新版本状态；
- 租约重启即失效、TTL 到期 fencing、永久租约阻止回收；
- 8 个写入器并发、跨崩溃重启与轮换，校验同一 `(key, version)` 下每个
  nonce 至多出现一次（`TestNoDuplicateNonceGlobal`）。

## 文件

- `noncelease.go`：类型、错误、nonce 编码；
- `allocator.go`：租约/高水位/TTL/轮换的并发安全状态机；
- `wal.go`：CRC 帧编码、重放截断、`*os.File` 持久化实现；
- `*_test.go`：故障注入磁盘与全部场景测试。
