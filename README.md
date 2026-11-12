# Noise XX 身份载荷绑定

本包实现 `Noise_XX_25519_ChaChaPoly_SHA256` 握手，并把每个对等节点的
Ed25519 身份绑定到本次握手的静态 X25519 公钥：

- 第二条和第三条消息携带身份载荷；
- 第一条消息只包含发起方临时公钥，任何附加载荷都会被拒绝；
- 身份载荷包含 Ed25519 公钥、Noise 静态公钥和签名；
- 签名覆盖 `h || staticPublicKey`，其中 `h` 是生成该消息载荷前的握手哈希；
- 只有握手完成且签名、哈希、静态公钥全部匹配后，身份才通过回调/查询接口发布。

载荷编码固定为 129 字节：

```text
version(1) || Ed25519 public key(32) || X25519 static key(32) || signature(64)
```

签名输入固定为 64 字节：

```text
SHA-256 handshake hash h(32) || X25519 static public key(32)
```

## 高层 Peer 用法

`Peer` 会自动在第二条/第三条消息中附加身份载荷，并在握手完成时验证对方身份。

```go
initStatic, _ := netsec.GenerateStaticKeyPair()
respStatic, _ := netsec.GenerateStaticKeyPair()
initID, _ := netsec.GenerateIdentityKeyPair()
respID, _ := netsec.GenerateIdentityKeyPair()

initiator, _ := netsec.NewPeer(netsec.PeerConfig{
    Role:            netsec.Initiator,
    StaticKeyPair:   initStatic,
    IdentityKeyPair: initID,
    OnPeerIdentity: func(id *netsec.PeerIdentity) error {
        // 仅在握手完成且载荷验证成功后调用。
        return nil
    },
})
responder, _ := netsec.NewPeer(netsec.PeerConfig{
    Role:            netsec.Responder,
    StaticKeyPair:   respStatic,
    IdentityKeyPair: respID,
})

msg1, _ := initiator.WriteMessage(nil)
responder.ReadMessage(msg1)
msg2, _ := responder.WriteMessage(nil)
initiator.ReadMessage(msg2)
msg3, _ := initiator.WriteMessage(nil)
responder.ReadMessage(msg3)

peer, err := initiator.PeerIdentity() // 此时才可读取对方身份
```

在握手完成前调用 `PeerIdentity` 返回 `ErrHandshakeIncomplete`；回调也不会执行。

## 底层完成回调校验

`HandshakeState` 暴露原始载荷和消息级绑定哈希。完成回调应使用
`VerifyPeerIdentity`，而不是自行使用最终 `HandshakeHash()`：

```go
if handshake.Complete() {
    identity, err := handshake.VerifyPeerIdentity(payload)
}
```

也可以直接调用无状态校验器：

```go
identity, err := netsec.ValidateIdentityPayloadAtCompletion(
    complete,
    bindingHash,
    peerStaticPublicKey,
    identityPayload,
)
```

`IdentityVerifier` 提供同样的状态化用法，并保证校验失败或握手未完成时
不缓存、不返回身份。

## 安全性质

- 替换静态公钥：载荷中签名的静态公钥必须等于握手中观察到的对方静态公钥，
  否则返回 `ErrStaticKeyMismatch`。
- 搬运身份载荷：签名覆盖消息级握手哈希；跨握手或把第二条消息载荷搬到
  第三条消息都会导致 `ErrInvalidIdentitySignature`。
- 第一条消息带载荷：写入和读取两侧都返回 `ErrFirstMessagePayload`。
- 未完成发布：`PeerIdentity`、回调及 `IdentityVerifier` 在完成前均不返回身份。

## 验证

```sh
go test ./...
```

测试覆盖：

- 合法完整 XX 握手及双方身份/静态公钥绑定；
- 第二条、第三条消息替换静态公钥；
- 第二条、第三条消息载荷跨握手搬运及跨消息搬运；
- 搬运载荷重加密进新握手时，`Peer` 不触发回调且保留校验错误；
- 第一条消息写入或注入载荷；
- 握手未完成时身份不可见；
- RFC 8439 ChaCha20、Poly1305 和 ChaCha20-Poly1305 测试向量。
