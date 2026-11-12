# RTSP会话与传输目标绑定

RTSP媒体服务器在SETUP里接受Transport头，攻击者可把destination指向受害者地址做反射，或猜测Session编号去控制他人的播放。给Go服务器写供会话管理调用的RTSP状态包：Session为128位随机值，绑定认证主体且只能由创建者使用，其他主体的PLAY和TEARDOWN返回454；destination与client_port只能等于请求来源地址和偶数起始端口对；CSeq在同一会话内必须递增。用destination改写、跨主体使用、CSeq倒退和合法点播验证没有媒体包发往他人地址。

## 使用

`netsec.Manager` 负责 SETUP、PLAY、TEARDOWN 状态，并在 SETUP 时冻结媒体目标：

```go
manager := netsec.NewManager()

setup := manager.Setup(
    principal,
    conn.RemoteAddr().String(),
    1,
    "RTP/AVP;unicast;client_port=5000-5001",
)
if !setup.OK() {
    return setup.WriteRTSP(w)
}

play := manager.Play(principal, conn.RemoteAddr().String(), setup.SessionID, 2)
if !play.OK() {
    return play.WriteRTSP(w)
}

if err := manager.WriteRTP(rtpConn, principal, setup.SessionID, payload); err != nil {
    return err
}
```

也可用 `HandleRaw` 处理完整报文；调用前必须由认证层填入认证主体和连接来源地址，不能信任客户端自报的地址。

## 安全约束

- Session ID 由 `crypto/rand` 生成 16 字节并编码为 32 位十六进制字符串。会话只接受创建时的认证主体，其他主体的 PLAY、TEARDOWN 和媒体写入均按会话不存在处理，返回 454。
- UDP SETUP 必须提供偶数起始、相邻的 `client_port` 对；`destination` 若出现，必须是等于请求来源 IP 的数字单播地址。TCP 模式使用偶数起始的相邻 `interleaved` 通道。
- 同一会话内所有后续请求的 CSeq 必须严格大于上一次成功请求的 CSeq。失败请求和被拒绝的跨主体请求不会推进 CSeq。
- `WriteRTP` / `WriteRTCP` 只能写入 SETUP 时冻结的目标地址和端口，不接受每次发送时重新指定 destination。

运行验证：

```sh
go test ./...
go test -race ./...
```
