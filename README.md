# RoCE连接报文准入

RoCE v2 的 RC 队列对没有加密认证，攻击者在同一网络上伪造源地址并猜 24 位的 QPN 与 PSN，就能向已建立的连接注入数据。为Go软 RNIC 模型写供入站报文处理调用的 QP 准入包：入站报文的源 GID 须等于连接建立时登记的对端 GID，目的 QPN 须对应该 QP，PSN 必须落在期望值附近的窗口内，窗口外丢弃且累计达阈值后把 QP 置为错误状态；QPN 随机分配，销毁后经隔离期才能复用。用错误源 GID、窗口边界、QPN 复用、阈值触发和合法重传验证。

## 设计

包 `netsec` 在软 RNIC 的入站处理路径上实现 RC 队列对准入，缓解伪造源地址 +
猜测 QPN/PSN 的注入面。

- 源 GID 校验：报文源 GID 必须等于 `Connect` 时登记的对端 GID，不符即丢弃
  （`DropGIDMismatch`）并单独计数，不计入 PSN 阈值。
- 目的 QPN 校验：目的 QPN 必须命中存活且处于连接态（RTR/RTS）的 QP，
  否则返回 `DropQPNNotFound` / `DropNotConnected`。
- PSN 窗口校验：PSN 必须落在期望 PSN 的回绕窗口
  `[expected-w, expected+w]`（闭区间，默认 ±64）内。24 位 PSN 按最短回绕
  距离比较，天然处理序号回绕。
- 越窗处置：窗口外报文丢弃（`DropPSNOutOfWindow`）并累计；累计达到阈值
  （默认 8）后 QP 转入 `StateError`，此后该 QP 一切报文都被拒绝。
- 重传/乱序：窗口内 `PSN < expected` 视为合法重传（`Verdict.Duplicate`），
  `PSN > expected` 视为乱序，二者均不回退期望 PSN；仅按序报文推进期望 PSN。
- QPN 生命周期：QPN 由 `crypto/rand` 在 24 位空间随机分配（保留号 0 不分配）；
  `DestroyQP` 后 QPN 进入隔离表，隔离期（默认 60s）内不会被重新分配，
  期满才重新可分配。时间源与随机源均可注入，便于确定性测试。

## 用法

```go
mgr := netsec.NewManager(netsec.Config{}) // 零值即默认配置

qpn, _ := mgr.AllocateQP()                       // 随机分配 24 位 QPN
peer := netsec.GIDFromAddr(netip.MustParseAddr("192.0.2.10"))
if err := mgr.Connect(qpn, peer, 0x100); err != nil { // 登记对端 GID/起始 PSN
    log.Fatal(err)
}

v := mgr.Admit(netsec.InboundPacket{
    SourceGID: peer,
    DestQPN:   qpn,
    PSN:       0x100,
})
if !v.Accepted {
    log.Printf("丢弃报文: %s", v.Reason)
}

_ = mgr.DestroyQP(qpn) // QPN 进入隔离期，期满前不可复用
```

## 验证

`go test -race ./...` 覆盖：错误源 GID 丢弃与计数、PSN 窗口边界（±w 接受、
±(w+1) 丢弃）、24 位回绕、合法重传标记、阈值触发错误态、错误态全拒、
QPN 随机分配与保留号、隔离期内禁止复用/期满复用、线性兜底扫描跳过存活与
隔离号等场景。
