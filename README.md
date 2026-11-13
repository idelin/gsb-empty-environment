# 抑制anyPolicy

inhibit_any_policy已经生效，anyPolicy节点仍被映射成具体策略。到达抑制深度后，anyPolicy不能再产生新的具体策略节点。未到抑制深度前的合法映射保留。可导入的Go库供证书路径调用。在抑制深度之后放一张带anyPolicy的证书。

## 可导入的 Go 库

模块 `example.org/netsec/q0271` 提供基于 RFC 5280 / RFC 9618 的证书路径策略校验：

```go
import netsec "example.org/netsec/q0271"

valid := netsec.PoliciesValid(chain, netsec.VerifyOptions{
    CertificatePolicies:   []netsec.OID{requiredPolicy},
    RequireExplicitPolicy: true,
})
```

`chain` 可以按叶子到信任锚或信任锚到叶子的顺序传入，库会自动识别并统一为叶子优先的顺序。`policiesValid` 保持与 Go `crypto/x509` 相同的叶子优先参数约定。

常用入口：

- `PoliciesValid` / `ValidatePolicies` / `VerifyPolicies`：布尔结果。
- `EvaluatePolicies`：返回 `Evaluation`，其中包含最终有效策略集合。
- `Validate` / `Verify`：返回 `ErrInvalidPolicy` 或 `nil`。
- `ValidatePEMChain`：解析 PEM/DER 后执行策略校验。
- `NewPolicyVerifier`：复用同一组选项执行多次校验。

## inhibitAnyPolicy 语义

- `inhibitAnyPolicy` 计数尚未到零时，证书中的 `anyPolicy` 可以按 RFC 9618 生成缺失的具体策略节点。
- 计数到零后，当前证书的 `anyPolicy` 断言不再生成任何新的具体策略节点。
- 抑制深度之前已经产生的合法策略节点和映射不会被后来的 `anyPolicy` 断言重新生成或覆盖。
- 抑制只限制 `anyPolicy` 代替具体策略；证书显式断言的具体策略仍然有效。
- `PolicyMappings` 中从 `anyPolicy` 映射到具体策略、或从具体策略映射到 `anyPolicy`，均视为无效。

## 回归测试

`policy_test.go` 和 `realcert_test.go` 覆盖抑制边界，并构造真实 DER 证书链：在抑制深度之后放置一张带 `anyPolicy` 的叶子证书。后者必须失败；如果抑制计数仍允许一张附加证书，则同一路径成功。

```sh
go test ./...
```

可选运行 Go 发行版自带的 NIST PKITS 策略向量：

```sh
RUN_NIST=1 go test -run TestNISTPKITSPolicy ./...
```
