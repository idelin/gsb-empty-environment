# leasecache — 基于租约的分布式缓存失效一致性保证器

用 Go 标准库实现的 AFS/Leases 风格一致性协议：

- 客户端向服务端请求特定文件的**读租约**（默认 5 秒），租约期内可**绝对放心**地读取本地缓存；
- 写客户端修改文件时，服务端先**回调通知**所有租约持有者撤销租约；
- 若持有者**失联**（崩溃/网络分区），服务端**安全阻塞至租约过期**后才放行写入 —— 保证**零脏读**。

## 结构

- `server.go` — 租约服务端：`/lease` 授予读租约；`/write` 阻塞至所有读租约撤销或过期后才应用修改
- `client.go` — 缓存客户端：本地缓存 + `/revoke` 撤销回调端口；`Disconnect()` 模拟失联
- `pipe.go` — 进程内内存网络（`net.Pipe`），与 TCP 相同的 Listen/Dial 语义；测试/演示用它跑标准 HTTP/JSON 协议，真实部署改用 TCP（`Server.Start` / `Client.Start`）即可，协议不变
- `leasecache_test.go` — `go test` 验收测试
- `cmd/demo/main.go` — 可运行的时间线演示

## 运行测试

```sh
go test -v
```

测试结果（本机 go1.11.4）：

```
=== RUN   TestAcceptanceWriteBlockedUntilLeaseExpiry
--- PASS: TestAcceptanceWriteBlockedUntilLeaseExpiry (5.17s)
=== RUN   TestWriteUnblockedEarlyByRevocation
--- PASS: TestWriteUnblockedEarlyByRevocation (0.01s)
=== RUN   TestWriteWithoutLeasesIsImmediate
--- PASS: TestWriteWithoutLeasesIsImmediate (0.00s)
=== RUN   TestExpiredLeaseDoesNotBlock
--- PASS: TestExpiredLeaseDoesNotBlock (0.54s)
PASS
ok  	leasecache	7.924s
```

其中 `TestAcceptanceWriteBlockedUntilLeaseExpiry` 即验收场景：客户端 1 在 t=0 获得
文件 X 的 5 秒读租约后失联；客户端 2 在 t=2s 发起写入；t=3s 时断言写入仍被阻塞、
客户端 1 读本地缓存仍是旧值（零脏读）、服务端版本号未变；写入在满 5 秒后才被放行。

## 运行演示

```sh
go run ./cmd/demo
```

实际输出：

```
[ 0.0s] === 场景 1：租约持有者失联，写入阻塞至租约过期 ===
[ 0.3s] server: 授予读租约: client=client-1 path=X lease=1 过期于 5s 后
[ 0.4s] client-1: READ X -> "v1" (version=1)，获得 5 秒读租约
[ 0.4s] client-1: 模拟失联（关闭回调端口），继续持有本地缓存
[ 2.5s] client-2: WRITE X = "v2" 发起，等待服务端放行...
[ 2.5s] server: 写入被阻塞: path=X 存在有效租约 lease=1 (client=client-1)，通知撤销
[ 2.7s] server: 撤销通知失败（client=client-1 疑似失联）: lease=1，将安全阻塞至租约过期
[ 3.5s] client-1: READ X -> "v1" (version=1)（本地缓存，租约仍有效，零脏读）
[ 5.3s] server: 写入生效: path=X version=2
[ 5.4s] client-2: WRITE 完成，X version=2
[ 5.5s] client-2: READ X -> "v2" (version=2)（写入已生效）
[ 5.5s] === 场景 2：租约持有者在线，撤销成功，写入提前放行 ===
[ 5.5s] client-3: READ X -> "v2" (version=2)，获得 5 秒读租约（保持在线）
[ 5.5s] server: 写入被阻塞: path=X 存在有效租约 lease=2/lease=3，通知撤销
[ 5.5s] server: 租约已撤销: client=client-2 / client=client-3
[ 5.5s] server: 写入生效: path=X version=3
[ 5.5s] client-2: WRITE X = "v3" 完成（version=3），仅等待 5ms（撤销回调成功，未等满 5 秒）
[ 5.5s] client-3: READ X -> "v3" (version=3)（缓存已被撤销，重新取租约）
```

## 协议要点

- 写路径的不变量：**只要存在任何未过期的读租约，写入绝不生效**。
- 撤销回调失败（连接拒绝/超时）时服务端不做任何乐观假设，一律等到租约过期 —— 用可用性换一致性。
- 客户端仅在本地时钟未超过租约过期时间时才使用缓存；过期后重新向服务端申请租约并刷新。
- 等待期间新授予的租约同样会被纳入撤销/等待集合（写循环每轮重新扫描租约表）。
