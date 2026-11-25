服务网格按证书SAN里的`*.prod`授予生产身份，攻击者用`evil.prod`的测试证书取得生产mTLS身份。请实现供Go服务网格控制面调用的可导入工作负载身份验证组件。身份由trust domain、SPIFFE路径、SAN集合、签名链、中间CA名称约束（Name Constraints）和节点证明共同决定；通配只允许出现在显式声明的最左完整标签且禁止与IDN Punycode混用或匹配多级子域，深度与主体用途受限，IP SAN、URI SAN和DNS SAN不得互相替代。证书吊销（OCSP/CRL 宽限窗与时间回拨拦截）、short-lived轮换和bundle更新按代次原子生效，调用方与目标身份策略在每次连接建立与会话恢复时重新评估。用`evil.prod`、多级通配、Name Constraints越界、URI冒充、跨trust domain、吊销后连接和合法同域服务验证身份不被放大。

## workloadid 组件

纯 Go 标准库实现的可导入工作负载身份验证组件（模块 `prescreen.local/ps-174fa6fd5f`，
包名 `workloadid`）。身份不依赖松散的 DNS 后缀，而是锚定在 SPIFFE trust domain
加上显式策略、签名链、中间 CA Name Constraints 与节点证明共同决定。

### 身份判定要素

- **Trust domain / SPIFFE 路径**：仅接受唯一的 `spiffe://` URI SAN，按 SPIFFE 规范
  校验 host 与路径段；跨 trust domain 一律拒绝（`identity.go`）。
- **SAN 集合严格分型**：DNS、IP、URI 分别存放与匹配，互不替代（`identity.go`、`policy.go`）。
- **签名链**：基于 bundle 根/中间池做 `x509.Verify`，限制链深度、叶子必须是终端实体且
  具备 DigitalSignature key usage，并约束 EKU 主体用途（`verifier.go`）。
- **Name Constraints**：除标准库校验外，对 DNS/URI/IP SAN 显式实现 RFC 5280 约束检查，
  遇到不支持的 DirectoryName 等约束类型直接失败关闭（`nameconstraints.go`）。
- **节点证明**：类型、主体、时效、nonce 防重放（`attestation.go`）。

### 通配匹配（修复 `*.prod` 放大）

`wildcard.go` 中 `DNSMatcher`：

- 通配只能是最左完整标签（`*.a.b` 合法，`a.*.b`、`a*.b` 拒绝）；
- 通配仅匹配一个标签，`*.x.example.com` 绝不匹配 `a.b.x.example.com`；
- 通配禁止与 IDN Punycode（`xn--`）混用；
- 锚点不足的 `*.prod` 这类模式在配置加载期即拒绝；身份本身也不来自 DNS 后缀。

### 吊销、轮换与代次

- **吊销**：自带最小 OCSP（RFC 6960）解析/签名验证（`internal/ocsp`）与 CRL 校验，
  支持宽限窗（grace）、过期陈旧（stale）失败关闭、`Unknown` 可配置为拒绝；
  OCSP certID 校验颁发者 NameHash/KeyHash，错误响应者签名被拒（`revocation.go`）。
- **时间回拨**：bundle 代次与验证器均维护墙钟高水位，证书 NotBefore/吊销时间戳超窗即
  判为回拨（`bundle.go`、`verifier.go`）。
- **代次原子生效**：`Manager` 通过原子指针切换不可变 `Snapshot`，bundle 更新与
  short-lived 凭证轮换各自成代次，旧代次不能覆盖新代次，TTL 超上限拒绝（`bundle.go`）。
- **每次连接重评估**：`TLSAuthorizer` 接入 `VerifyConnection`；默认关闭会话票据，
  开启恢复时仍对最新代次重新执行吊销与调用方/目标策略（`tls.go`）。

### 验证的“身份不被放大”场景

测试位于 `*_test.go`，覆盖：

1. `evil.prod` 测试证书：链不受信，且即便信任其根也因跨 trust domain 被拒，`*.prod` 不授予身份；
2. 多级通配子域不匹配，单标签通配正常；
3. Name Constraints 越界（excluded/permitted，DNS/URI/IP）拒绝；
4. 非 SPIFFE URI 无法冒充身份，SAN 类型不能互相替代；
5. 跨 trust domain 拒绝；
6. OCSP/CRL 吊销后连接（含会话恢复）被拒，宽限窗/陈旧/回拨行为正确；
7. 合法同域服务通过且身份不超出策略边界。

### 运行

```sh
go test ./...        # 单元 + 真实 mTLS(net.Pipe) 集成测试
go test -race ./...  # 竞态检测
```
