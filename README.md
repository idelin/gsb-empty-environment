# 预密钥握手把身份键换到另一会话

异步消息发起方从目录取预密钥包建立会话，若转录没绑定双方身份和一次性预密钥ID，攻击者可换包或形成未知密钥共享。实现供Go消息宿主调用的题内P-256预密钥握手：双方设备均有登记的Ed25519签名公钥和P-256身份密钥IK；接收方包含其Ed25519签名公钥、IK、带ID的签名预密钥SPK、可选一次性预密钥OPK及过期时刻，Ed25519签名覆盖固定域标签、accountID、deviceID、IK、SPK ID/点、OPK ID/点和过期时刻；目录证明回调确认包属于目标设备。发起方生成临时EK，计算DH(EK,IK)、DH(EK,SPK)、DH(自身IK,SPK)及有OPK时DH(EK,OPK)，拒绝非法/无穷点，将定长共享点编码按固定顺序经HKDF-SHA256派生rootKey，salt为转录摘要；转录绑定双方account/deviceID、两IK、EK、所有prekey ID及目录证明摘要。首条密文AEAD附加数据为转录，并含发起方Ed25519签名。接收方原子消费OPK后才交付首条明文，同一握手重传返回原结果，换EK/转录拒绝；无OPK明确降低一次性属性但仍可建链。用真实曲线测试换身份键、SPK搬运、OPK重用、目录目标错配、过期包和响应丢失，不能把目录包验签等同于对端在线。

## 宿主接入

```go
alice, _ := netsec.NewDeviceIdentity("alice", "phone")
bob, _ := netsec.NewDeviceIdentity("bob", "laptop")

spk, _ := netsec.NewSignedPreKey(7)
opk, _ := netsec.NewOneTimePreKey(9)
receiver, _ := netsec.NewReceiver(netsec.ReceiverConfig{
	Identity:       bob,
	SignedPreKey:   spk,
	OneTimePreKeys: []*netsec.OneTimePreKey{opk},
	ExpiresAt:      time.Now().Add(24 * time.Hour),
})
bundle, _ := receiver.PreKeyBundle(opk.ID)

initiator, _ := netsec.NewInitiator(netsec.InitiatorConfig{
	Identity: alice,
	DirectoryProof: func(ctx context.Context, target netsec.PublicIdentity, b *netsec.PreKeyBundle) ([]byte, error) {
		// 查目录并确认 b 的 account/device、IK、SPK、OPK 均属于 target。
		return directory.Attest(ctx, target, b)
	},
})
handshake, _ := initiator.Start(bob.Public(), bundle, []byte("hello"))

result, _ := receiver.AcceptInitial(handshake.Message(), alice.Public())
// result.Plaintext、result.RootKey、result.Mode、result.OneTimePreKeyUsed
```

响应丢失时重发 `handshake.MessageBytes()` 的原始字节。接收方按转录摘要缓存会话；完全相同消息返回原 `ReceiveResult`（`Replayed=true`），不会二次消费 OPK。不同 EK 的新握手若复用同一 OPK，返回 `ErrOneTimePreKeyUsed`。

## 安全语义

- `PreKeyBundle` 的 Ed25519 签名覆盖版本、固定域标签、account/device、接收方 Ed25519 公钥、IK、SPK ID/点、OPK 存在标志/ID/点及纳秒过期时刻。`Start` 只接受签名有效、未过期且与调用方登记的目标 `PublicIdentity` 完全一致的包。
- 目录证明回调拿到完整包和目标身份，宿主应校验预密钥确实属于该设备；仅验 bundle 签名不足以阻止攻击者用自己的签名密钥重签搬运来的 SPK。目录证明只证明发布归属，不代表设备在线或已读消息。
- 转录绑定双方 account/device、双方 Ed25519 公钥与 IK、EK、SPK ID/点、OPK 存在标志/ID/点、包过期时刻和 `SHA-256(directoryProof)`。首条明文用 `AES-256-GCM`，AAD 为转录规范字节。
- rootKey 输入固定为 `DH(EK,IK_R) || DH(EK,SPK_R) || DH(IK_A,SPK_R) [|| DH(EK,OPK_R)]` 的 32 字节 x 坐标，按顺序拼接；HKDF-SHA256 的 salt 为转录摘要，info 为固定域标签。
- 接收方在返回首条明文前，在同一互斥区内完成 OPK 已用标记、结果缓存和会话登记。无 OPK 时 `Mode` 为 `PreKeyModeSignedPreKeyOnly`，明确表示缺少一次性贡献。
- 所有 P-256 输入必须是规范未压缩点；nil、压缩编码、曲线外点和无穷点均拒绝。P-256 identity/signed/one-time 私钥也不接受 X25519 等异曲线密钥。

当前 `Receiver` 的 OPK 消费与重传缓存是进程内状态。宿主若跨进程/重启部署，需把已消费 OPK 和握手结果写入同一事务性存储后再交付明文。

## 测试

`handshake_test.go` 使用真实 P-256/Ed25519 曲线覆盖往返、无 OPK、换身份键、SPK 搬运、OPK 重用与并发消费、目录目标错配、过期包、首条密文/转录篡改、重传响应丢失及规范编码。

```sh
go test ./...
go test -race ./...
```
