# MACsec安全通道标识绑定

MACsec接收端若只按源MAC选通道，攻击者可伪造SecTAG里的SCI并注入帧。为Go二层安全组件实现接收通道表，供解封装前调用：SCI由对等MAC与端口号组成，只有MKA已通告且与密钥标识KI绑定的SCI才可接收，ICV通过后才更新32位包号窗口，不递增或超窗口的帧丢弃。用伪造SCI、旧SAK、重放帧和合法重键切换验证。

## 设计

包 `netsec` 实现解封装前调用的接收通道表 `RxTable`，把“选通道”与“授权”
绑定在一起，避免仅凭源 MAC 选择安全通道：

- `SCI`（`sci.go`）：6 字节对等 MAC + 2 字节端口号，8 字节大端编码，值类型
  可直接作为 map 键。
- 通道授权：`LearnSCI` 只登记 MKA 已通告的 SCI；`InstallSA` 在该 SCI 的
  关联号（AN，0..3）上绑定 KI（`KeyID`）与 SAK。未通告的 SCI 返回
  `ErrUnknownSCI`，未绑定 KI/SAK 的 AN 返回 `ErrNoSecureAssociation`，
  从根上拒绝伪造 SCI。
- 密钥校验：`RxSA.VerifyKeyID` 校验帧携带的 KI 是否与 MKA 绑定的一致，
  不一致返回 `ErrStaleSAK`；`RetireSA` 废弃旧 SAK 后，命中该 AN 的帧返回
  `ErrRetiredSAK`。
- 重放保护：每个 SA 独立维护 32 位包号滑动窗口（位图，默认 64，可用
  `WithReplayWindow` 调整为 1..64）。`CheckPN` 只判定不修改状态，`Commit`
  在 ICV 通过后才推进窗口；重复/不递增/超出窗口返回 `ErrReplay`，包号 0
  返回 `ErrInvalidPacketNumber`。采用有符号 32 位差值，正确处理包号回绕。
- 重键：`Rekey` 采用 make-before-break，在新 AN 安装新 KI/SAK 时保留旧
  AN，切换重叠期两者都可收；`RetireSA` 完成切换后旧 SAK 立即失效。

## 接收流程

分步接口（推荐，便于在 ICV 计算前后插入真实解密逻辑）：

```go
table := netsec.NewRxTable()
table.LearnSCI(sci)
table.InstallSA(sci, 0, ki, sak)

sa, err := table.Lookup(tag.SCI, tag.AssociationNumber) // 伪造 SCI 在此被拒
if err != nil { return err }
if err := sa.VerifyKeyID(presentedKI); err != nil { return err }
if err := sa.CheckPN(tag.PacketNumber); err != nil { return err } // 不改状态
icvOK := verifyICV(frame, sa.SAK())
if !icvOK { return netsec.ErrICV }                            // 窗口不更新
return sa.Commit(tag.PacketNumber)                             // ICV 通过后推进
```

一站式接口（调用方已完成 ICV 计算）：

```go
if err := table.Receive(tag, icvOK); err != nil {
    // 丢弃帧并按错误类别计数
}
```

## 测试

`go test -race ./...` 覆盖四类验收场景及边界：

- 伪造 SCI：未知源 MAC、已知 MAC 但未通告端口、越界 AN、未绑定 AN；
- 旧 SAK：`RetireSA` 后旧 AN 帧被拒，槽位可重新下发新 SAK，旧 KI 被拒；
- 重放帧：重复、不递增、超窗口丢弃，ICV 失败不推进窗口，包号回绕正确；
- 合法重键切换：重叠期新旧 AN 并存，切换后旧 AN 失效、新 AN 独立计数。
