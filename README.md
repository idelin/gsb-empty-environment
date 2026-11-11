# JWT多签名阈值与用途

设备更新请求采用多个签名共同批准，别名密钥和重复签名不能增加票数。为Go接收服务实现可导入的批准包，调用方提供已解析消息和公钥集合，签名覆盖版本、设备组、操作摘要与截止时间；按公钥材料去重计票，管理员与设备签名分别满足阈值，未知算法拒绝。使用标准库真实签名测试角色混用、重复别名、内容拼接及过期边界，不提供自定义密码算法。

## `approval` 包

导入路径：`example.org/netsec/uid1265/approval`

调用方负责解析报文并组装 `approval.UpdateRequest` 与 `approval.KeySet`，接收方调用
`approval.Verify(now, req, keys, policy)`：

- 签名只使用标准库算法：`ED25519`、`ES256`（P-256/SHA-256）、`ES384`（P-384/SHA-384）、
  `PS256`/`PS384`（RSASSA-PSS）；其他算法返回 `ErrUnknownAlgorithm`。
- 签名内容通过 `SigningBytes` 以固定域分隔前缀、字段长度前缀和绝对截止时间（Unix 秒+纳秒）
  确定性拼接，覆盖版本、设备组、操作摘要与截止时间，防止字段拼接歧义。
- 角色仅由受信 `KeySet` 中的公钥条目决定，`Signature.ClaimedRole` 不被信任，无法提权。
- 计票以 `x509` DER 公钥材料去重：同一公钥的重复签名或多个别名只投一票；管理员与设备分别
  按 `Thresholds` 校验，不足返回 `ErrUnauthorized`。
- 过期判定以调用方提供的 `now` 为时钟：`now == Deadline` 仍接受，`now > Deadline` 返回
  `ErrExpired`，边界可直接单测。

运行测试：`go test ./...`（测试用标准库生成 Ed25519/ECDSA/RSA 真实密钥并真实签名）。
