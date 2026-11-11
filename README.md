# 数据报密钥阶段切换

加密传输接收端允许短期保留前代密钥，但伪造数据包不能推动密钥更新。写供Go传输宿主调用的接收包，数据报携带密钥阶段位和截短序号，截短位宽由输入给定，完整号取与最高已认证号加一距离最近的合法候选，并列取较小者；认证回调成功后才能推进阶段和重放窗口。旧代只接受切换边界以前的包，相同阶段位两次回绕不能混用旧钥。用可控认证器测试乱序、坏标签、回绕和并发重复包。

## 用法

`Receiver` 由传输宿主构造，注入一个 `Authenticator` 回调：

```go
rx, err := netsec.New(netsec.Width2, netsec.Key(initialKey), hostAuth)
if err != nil { /* 位宽非法等 */ }

// 宿主派生并预置下一代密钥；阶段不会因此立即推进。
rx.RotateKey(netsec.Key(nextKey))

pkt, err := rx.Receive(datagram)
// pkt.Number 为还原后的完整序号，pkt.Generation 为实际认证所用密钥代次。
// 只有 hostAuth.Authenticate 返回 nil 后，阶段、最高序号与重放窗口才会推进。
```

线格式首字节为密钥阶段位（`WirePhaseBit`），随后 `width` 字节为网络序截短序号，
其余为载荷。可使用 `EncodeDatagram`/`ParseHeader` 编解码。

## 测试

`go test -race ./...` 使用可控 HMAC 认证器覆盖乱序、坏标签不推进状态、
单字节回绕、旧代切换边界、同相位两次回绕拒绝以及并发重复包仅接受一次。
