# 路径循环

同一主体和公钥在路径中出现两次，长度计数被绕开。主体可区分名加公钥的组合不能重复；重复则失败。各张证书主体不同的路径继续。可导入的Go库供证书路径调用。把第一张中间证书在末尾再放一次。

## 用法

```go
import netsec "example.org/netsec/q0275"

path := []*x509.Certificate{leaf, intermediate, root}
if err := netsec.ValidatePath(path); err != nil {
    // 主体可区分名与公钥组合重复时，errors.Is(err, netsec.ErrDuplicateSubjectPublicKey) 为 true。
}
```

`ValidatePath`（以及 `Validate`、`VerifyPath`、`CheckPath`）接受 `[]*x509.Certificate`，
并在整条路径上检查重复的主体可区分名与公钥组合。该检查忽略 SAN、序列号、有效期等差异；
仅主体相同或仅公钥相同的证书不会被视为重复。

需要密码学校验时，可以额外传入 `netsec.WithSignatureVerification()` 或
`netsec.WithTrustVerification(roots)`。
