# sshcert — 原生 OpenSSH 证书与 KRL 校验包

堡垒机与 SSH 认证宿主在校验 OpenSSH 用户/主机证书
（`ssh-ed25519-cert-v01@openssh.com`）与二进制密钥吊销列表（KRL
`SSHKRL` 格式）时，若解析器放过未知关键选项（`critical options`）、错误忽略
证书序列号区间吊销或未按字典序校验 KRL 子段，已被吊销或越权指定
`source-address` 的凭证仍可登录生产主机。

本包为 Go 认证宿主提供可导入的**纯标准库**实现，严禁并确实不依赖
`golang.org/x/crypto/ssh` 的证书与 KRL 解析；生产代码仅使用
`crypto/ed25519`、`crypto/sha256` 及 `net`、`sort` 等标准库。

## 导入

```go
import "formal.local/w27-ede4350407/sshcert"
```

## 证书能力

`ParseCertificate` 从原始线格式字节完整解码全部字段：`nonce`、内嵌公钥、
`serial`、`type`、`key id`、`valid principals`、`valid after/before`、
`critical options`、`extensions`、`reserved`、`signature key`、`signature`，
并要求缓冲区被精确消费（拒绝截断与尾随字节）。

`Verify` 用真实 Ed25519 对「签名长度前缀之前的全部证书字节」验签，并按序强制：

- 证书类型与请求服务（用户/主机）一致；
- 未知 `critical options` 一律拒收（仅内建 `force-command`、
  `source-address`、`verify-required`，宿主可用 `AllowedCriticalOptions`
  显式扩展，绝不静默忽略）；
- 内嵌签名 CA 必须与宿主信任的 CA blob 完全相等，否则 `ErrCAKeyMismatch`；
- `valid_after <= now < valid_before`（零边界表示无界，语义对齐 OpenSSH）；
- 请求主体必须命中 `valid_principals`；空列表默认拒绝，仅当宿主显式设置
  `AllowWildcardPrincipal` 才允许通配；
- `source-address` 的 CIDR 或裸 IP 列表必须覆盖连接源 IP，畸形条目直接拒收；
- `force-command` 存在时通过 `VerifyResult.ForceCommand` 回传不可覆盖命令，
  畸形编码按证书错误处理而非丢弃。

## KRL 能力

`ParseKRL` 完整解析 `SSHKRL` 魔数、版本、生成时间、注释与全部段：

- 顶层 `CERTIFICATES(1)`、`EXPLICIT_KEY(2)`、`FINGERPRINT_SHA1(3)`、
  `SIGNATURE(4)`、`FINGERPRINT_SHA256(5)`，严格按类型号升序且不得重复；
  未知段类型直接报错；
- `CERTIFICATES` 段按 CA 公钥 blob 分组（空 CA blob 为通配所有 CA），
  每个 CA 记录含协议要求的 `reserved` 字符串；
- 子段 `SERIAL_LIST(0x20)`、`SERIAL_RANGE(0x21)`、
  `SERIAL_BITMAP(0x22)`、`SERIAL_KEY_ID(0x23)`，类型只许非递减；
- 序列号列表严格升序；区间升序且不重叠，`max=0` 表示开区间且必须收尾；
  位图为 `uint64 offset + mpint`，校验 mpint 规范编码（非负、最短表示），
  偏移严格升序、位图非空；`key_id` 严格升序，空字符串通配且不得与其他条目并存；
- 显式公钥段与 SHA-256 指纹段严格字典序，指纹按 32 字节定宽校验；
- 所有声明长度都做越界检查，截断、乱序位图、乱序/重复段、畸形记录全部拒收。

`KRL.CheckCertificate` 覆盖全部吊销路径：显式公钥 blob、公钥 blob 的
SHA-256 指纹、CA 维度的序列号列表/区间/位图（**序列号 0 作为真实序列号同样
可吊销**）、位图偏移位、精确 `key_id` 与空 `key_id` 通配。任意命中返回
包装 `ErrRevoked` 的错误，可直接接入 `VerifyOptions.KRL` 在认证链末端拦截。

## 快速使用

```go
cert, err := sshcert.ParseCertificate(certWireBytes)
if err != nil { /* 拒绝 */ }

krl, err := sshcert.ParseKRL(krlBytes) // 启动时解析一次，复用
if err != nil { /* KRL 文件不可信，按策略失败关闭 */ }

res, err := sshcert.Verify(cert, sshcert.VerifyOptions{
    ExpectedType:    sshcert.CertTypeUser,
    CAPublicKeyBlob: trustedCABlob,
    Principal:       requestedUser,
    RemoteIP:        conn.RemoteAddr,
    Now:             time.Now().Unix(),
    KRL:             krl,
})
if err != nil { /* 拒绝登录 */ }
if res.ForceCommandPresent {
    // 强制执行 res.ForceCommand，忽略客户端自带命令
}
```

## 测试

测试不依赖任何外部序列化器：所有证书与 KRL 均由测试内的线格式写入器
逐字节手工构造，并以真实 Ed25519 签名。覆盖：

- 全字段解码、签名通过/CA 不匹配/签名篡改/截断与尾随/非法类型；
- 未知关键选项拒收与显式 opt-in、时效边界、主体通配开关、类型不匹配；
- `source-address` IPv4/IPv6 CIDR 与裸 IP 匹配、拒绝、缺源 IP、畸形条目；
- `force-command` 回传与畸形编码拒收；
- KRL 全段命中（显式公钥、SHA-256、序列号 0、区间含开区间、mpint 位图、
  key_id 与通配、多 CA 隔离、通配 CA）；
- 攻击向量：魔数/版本错误、越界长度、截断、未知/重复/乱序段、乱序 CA 与
  位图、重叠区间、非规范/负数 mpint、ragged 指纹等。

```sh
go test -race ./...
```
