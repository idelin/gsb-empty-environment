# 身份未进入DH转录导致未知密钥共享

点对点协议给临时DH值签名，却没把双方角色身份放进签名和密钥派生，攻击者可让两端对共享密钥的对端身份认知不同。为Go协议测试宿主实现本题P-256握手包：发起方A发送会话ID与临时点X=xG，响应方B发送Y=yG及Ed25519签名；加固转录按固定域标签、会话ID、发起方身份、公钥、响应方身份、公钥、X、Y的长度前缀字节串编码，B签名其SHA256，A随后签名同一摘要。双方验签成功后，以共享点xyG的规范压缩编码为输入密钥材料，HKDF-SHA256的salt为转录摘要，info分别为send-A、send-B和confirm，派生方向密钥及确认密钥；双方交换HMAC确认值绑定角色标签和转录。身份注册表由宿主固定，身份名为非空UTF-8字节串并唯一映射Ed25519公钥，不接受报文自带替换键。另实现待审的弱版本，签名只覆盖X、Y且HKDF不含身份。给至多3个并发会话的攻击者路由模型，允许转发、改会话外层身份标签但不能伪造签名，搜索弱版本中双方密钥相同却记录不同对端身份的最短轨迹，并证明加固版本在同动作集拒绝。覆盖同一点X=Y只允许在点合法且双方各自持有对应临时私钥时进入后续验证；覆盖反射X=Y、点非法、会话ID复用和确认值顺序。

## 实现概览

- `identity.go`：宿主固定的 `Registry`，身份名必须是合法非空 UTF-8 且与 Ed25519 公钥双向唯一；`Party` 校验本地私钥与注册公钥一致。
- `point.go`：P-256 点解析、压缩编码、ECDH 与 `X == Y` 规则。非法点、非规范编码和无穷远点在进入状态机前拒绝。
- `transcript.go`：`Transcript` 的 4 字节大端长度前缀编码。加固模式覆盖域标签、会话 ID、双方身份名/公钥、X、Y；弱模式故意只覆盖 X、Y。
- `kdf.go`：RFC 5869 HKDF-SHA256，info 为 `send-A`、`send-B`、`confirm`。
- `handshake.go`：A/B 状态机、签名、确认顺序和会话 ID 消耗。A 先确认，B 验证 A 后发送 B 确认；双方确认完成前不释放流量密钥。
- `attack.go`：最多 3 个并发会话的确定性攻击者路由，支持转发、改写外层身份标签，以及用攻击者自有身份签名，但不能伪造 A/B 签名。
- `*_test.go`：覆盖正常握手、身份重绑定、弱版本 UKS 轨迹、加固拒绝、反射 `X=Y`、非法点、会话复用和确认顺序等。

## 最短轨迹

弱版本的最短 UKS 轨迹为 6 步：

1. A 发起 A -> B 会话，发送 X。
2. 路由把 Hello 的外层发起方标签改成 M，转发给 B。
3. B 的 Y/签名回到 A；弱签名只覆盖 X、Y，因此 A 接受。
4. 路由用 M 自己的密钥对同一 X、Y 摘要签名，B 以 M 为对端接受。
5. A 的确认转发给 B。
6. B 的确认转发给 A。

结果：A 与 B 得到相同密钥，但 A 记录对端为 B，B 记录对端为 M。加固版本在第 3 步因 B 的签名覆盖了 A/B 身份而失败；`ProveHardenedRejects` 还在同一动作集上做穷举检查。

## 测试

```sh
go test ./...
go test -race ./...
```

## 最小用法

```go
registry := netsec.NewRegistry()
alice, _ := netsec.GenerateParty("A", registry, nil)
bob, _ := netsec.GenerateParty("B", registry, nil)

keys, err := netsec.RunHandshake(alice, bob, []byte("session-1"), netsec.ModeHardened)
if err != nil {
    // Pairwise-role/protocol rejection
    panic(err)
}
// keys.SendA, keys.SendB, keys.Confirm
```
