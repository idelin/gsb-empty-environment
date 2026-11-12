# IKE_AUTH签名覆盖核对

IKEv2对端若不核对AUTH载荷覆盖的内容，攻击者可替换身份载荷或反射发起方自己的消息。为Go VPN网关实现IKE_AUTH校验包，供第二轮交换调用：AUTH签名输入为RealMessage1、对端随机数和PRF(SK_p,身份载荷体)，响应方身份须与证书SAN一致，收到与己方相同的随机数或自己的IDi则拒绝。用换ID、反射消息和合法交换验证。

## 用法

第二轮交换直接使用 `Exchange`，其中 `RealMessage1` 必须是发起方原始
IKE_SA_INIT 报文的完整字节，不能重新序列化：

```go
ex := &netsec.Exchange{
    RealMessage1:         realMessage1,
    InitiatorNonce:       ni,
    ResponderNonce:       nr,
    InitiatorID:          idiPayloadBody, // IDType + 3 个保留字节 + 身份数据
    ResponderID:          idrPayloadBody,
    InitiatorSKp:         skPi,
    ResponderSKp:         skPr,
    ResponderCertificate: peerCert,
    PRF:                  netsec.HMACPRF(sha256.New),
}

// 发起方校验响应方 IKE_AUTH 响应中的 AUTH。第三个参数是 AUTH 签名/MAC
// 本身（已去掉 AUTH 载荷开头的 Auth Method 字节）。
err := ex.VerifyResponderAuth(netsec.AuthMethodSignature, nil, authSignature)
```

若手上是完整 AUTH 载荷体，可调用
`params.VerifyAuthPayload(method, key, payloadBody)`，它会先剥离开头的
Auth Method 字节再执行上述校验。

校验器计算的签名输入严格为：

```
RealMessage1 || PeerNonce || PRF(SKp, PeerIdentityPayloadBody)
```

`PeerIdentity` 是正在认证的一方的完整 ID 载荷体。响应方校验强制要求
`ResponderCertificate` 非空，并将 ID_FQDN/ID_IPV4_ADDR/ID_IPV6_ADDR/
ID_RFC822_ADDR 与证书 subjectAltName 比对，不回退到 Subject CN。

在密码学校验前还会拒绝以下反射/替换：

- `PeerNonce == OwnNonce`：对端随机数与本端相同。
- `PeerIdentity == OwnIdentity`：收到本端自己的 ID（包括 FQDN 大小写、
  末尾点号等价形式）。

`AuthParams` 是所有检查的底层入口；HMAC/PSK 模式可用
`AuthMethodHMAC`，证书签名模式支持 RSA PKCS#1 v1.5、RSA-PSS、ECDSA、
DSA 和 Ed25519。证书链、有效期和吊销状态需由调用方在接入此包前完成，
本包只负责 AUTH 覆盖范围和 SAN 身份绑定。

## 测试

```
go test -race ./...
```
