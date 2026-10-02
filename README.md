# leasecache — 基于租约的分布式缓存失效一致性保证器

类似 AFS/Leases 协议的读租约一致性组件，纯 Go 标准库实现（`net/rpc` + 可插拔传输层）。

## 协议语义

- 客户端通过 `ReadFile` 向服务端申请文件的**读租约**（默认 5 秒）。
- 租约有效期内，客户端**绝对放心地读本地缓存**，不再回源。
- 写客户端调用 `WriteFile` 时，服务端先**回调撤销**该文件的所有活跃租约：
  - 客户端在线：确认撤销（缓存失效），写者**立即放行**；
  - 客户端失联：服务端**安全阻塞至租约自然过期**后才放行写入 —— 零读脏。
- 写者等待期间暂停发放新租约，保证写者等待时间上界为一个租约周期。

## 结构

- `lease/server.go` — 租约服务端：租约授予、撤销回调、写阻塞至租约失效
- `lease/client.go` — 缓存客户端：本地缓存、撤销回调处理、失联模拟（`Disconnect`）
- `lease/transport.go` — 传输抽象：`NetTransport`（TCP/Unix，生产用）与 `MemTransport`（`net.Pipe` 进程内网络，测试/演示用）
- `lease/lease_test.go` — `go test` 测试（含验收场景）
- `cmd/demo/main.go` — 可运行样例

## 运行样例

```sh
go run ./cmd/demo
```

输出：

```
=== 租约服务端已启动 mem-1（租约时长 5s） ===

--- 场景 A：客户端 1 在线（撤销回调成功） ---
[t= 0.0s] 客户端 1 获得 X 的读租约（5s），内容缓存到本地
[t= 2.0s] 客户端 2 请求写入 X ...
[t= 2.0s] 写入生效（等待 1ms）：服务端回调撤销 -> 客户端 1 确认 -> 立即放行
[t= 3.0s] 客户端 1 再次读 X：cache-hit=false 内容="v2"（缓存已被撤销，回源读到新值）

--- 场景 B：客户端 1 失联（撤销回调无法送达） ---
[t= 0.0s] 客户端 1 获得 X 的读租约（5s），随后失联
[t= 2.1s] 客户端 2 请求写入 X ...（服务端撤销通知失败，安全阻塞等待租约过期）
[t= 5.0s] 写入生效（阻塞 2.96s）：租约自然过期，零读脏保证成立
[t= 5.0s] 新客户端读 X：内容="v3"（写入已生效）
```

## 测试

```sh
go test -v -count=1 -race ./lease/
```

结果（全部通过，含 race 检测）：

```
=== RUN   TestAcceptance                # 验收：5s 租约，t=2s 写请求，客户端失联，写入恰在 t≈5.01s 放行
    lease_test.go:89: write unblocked at 5.010064792s (lease expired at 5s)
--- PASS: TestAcceptance (5.01s)
=== RUN   TestRevocationFastPath        # 在线客户端：撤销确认后写者立即放行（10s 租约下 0s 完成）
--- PASS: TestRevocationFastPath (0.00s)
=== RUN   TestCacheHitWithinLease       # 租约期内重复读全部命中本地缓存（服务端只见 1 次读）
--- PASS: TestCacheHitWithinLease (0.00s)
=== RUN   TestWriterBlocksNewLeases     # 写者等待期间新租约阻塞，写入完成后读者直接读到新值
--- PASS: TestWriterBlocksNewLeases (0.50s)
=== RUN   TestLeaseExpiryRefreshesCache # 租约自然过期后客户端回源，读到已生效的写入
--- PASS: TestLeaseExpiryRefreshesCache (0.45s)
PASS
ok      leasecache/lease        7.364s
```

## 接入真实网络

```go
tr := lease.NetTransport{Network: "tcp", ListenAddr: ":9000"}
ln, _ := tr.Listen()
srv := lease.NewServer(5*time.Second, tr)
srv.Serve(ln)
// 客户端：lease.NewClient("app-1", "server:9000", tr)
```
