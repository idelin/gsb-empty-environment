# RTSP会话与传输目标绑定

RTSP媒体服务器在SETUP里接受Transport头，攻击者可把destination指向受害者地址做反射，或猜测Session编号去控制他人的播放。给Go服务器写供会话管理调用的RTSP状态包：Session为128位随机值，绑定认证主体且只能由创建者使用，其他主体的PLAY和TEARDOWN返回454；destination与client_port只能等于请求来源地址和偶数起始端口对；CSeq在同一会话内必须递增。用destination改写、跨主体使用、CSeq倒退和合法点播验证没有媒体包发往他人地址。

## rtsp 包

实现位于 `rtsp/`，仅依赖标准库，核心 API：

- `Manager.Setup(principal, source, transportHeader, cseq)`：校验 Transport
  头并创建会话，把媒体目标钉死为请求来源地址与校验通过的 `client_port`
  端口对，Session ID 由 `crypto/rand` 生成 128 位随机值。
- `Manager.Play` / `Manager.Teardown(principal, id, cseq)`：会话不存在或
  非创建者主体一律返回携带状态码 454 的 `*rtsp.Error`；CSeq 必须严格递增，
  否则返回 400。
- `Manager.Deliver(id, sink, packet)`：仅在 PLAY 之后生效，且只发往 SETUP
  时钉住的目标，TEARDOWN 后或外来源 ID 都不会触达 `PacketSink`。
- `ParseTransport(header, source)`：`destination` 必须等于来源地址（缺省
  按 RFC 2326 取来源地址），`client_port` 必须是偶数端口加连续奇数端口，
  multicast 直接拒绝。

典型调用：

```go
mgr := rtsp.NewManager()
src, _ := rtsp.AddrFromNetAddr(req.RemoteAddr) // 信任观察到的来源
sess, err := mgr.Setup(rtsp.Principal(authUser), src, req.Get("Transport"), cseq)
if err != nil { /* 用 err.(*rtsp.Error).Status 回响应 */ }

id, _ := rtsp.ParseSessionID(req.Get("Session"))
if _, err := mgr.Play(rtsp.Principal(authUser), id, cseq); err != nil { /* 454/400 */ }

// 媒体循环：目标地址由管理器决定，请求方无法再改写
_ = mgr.Deliver(sess.ID, udpSink, rtpPacket)
```

## 验证

`rtsp/manager_test.go` 覆盖四类要求：destination 改写反射、跨主体
PLAY/TEARDOWN、CSeq 倒退（含相等/回退/0）、合法点播全链路，并断言录制型
`PacketSink` 收到的每个包都只发往创建者的钉住地址；另有端口对、128 位
Session ID、IPv6 与并发竞态测试。运行：

```sh
go test -race ./...
```
