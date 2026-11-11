# deltacrl

证书校验服务每天拉取完整CRL，每小时只拉增量CRL。给Go校验服务实现吊销状态组件：增量CRL引用的BaseCRLNumber不能晚于当前基础CRL，removeFromCRL能解除基础中的certificateHold，签发者和分发点范围必须一致，任一份过期都判为状态未知。用自建CA签出基础与增量CRL序列，核对乱序到达、基础更新后的旧增量和暂停解除的结果。

## 组件

`Checker`（见 `deltacrl.go`）绑定一个 CRL 签发 CA，维护该范围的基础 CRL、
增量 CRL 序列与合并后的吊销视图，线程安全。

- `NewChecker(issuer)`：传入 CRL 签发 CA 证书。
- `IngestDER(der)`：接入一份 DER CRL，自动识别基础/增量并完成全部校验。
- `Check(serial)`：返回 `Good` / `Revoked` / `Unknown` 三态结果。

## 校验规则（RFC 5280）

1. **签名与签发者**：CRL 必须由绑定 CA 验签通过，且 `issuer` DN 与 CA 主体一致，
   否则分别返回 `ErrBadSignature` / `ErrIssuerMismatch`。
2. **基础引用（§5.2.4）**：增量 CRL 的 `baseCRLNumber` 晚于当前基础 CRL 序号时
   返回 `ErrFutureBaseReference`；增量自身序号不新于基础或与已有增量重号时
   返回 `ErrStaleCRL`。
3. **范围一致（§5.2.5）**：增量与基础的 `issuingDistributionPoint`
   （fullName、onlyUser/CA、onlySomeReasons、indirectCRL）必须逐项一致，
   否则返回 `ErrScopeMismatch`。
4. **暂停解除（§5.3.3）**：`removeFromCRL` 只能解除 `certificateHold`；
   目标为其他吊销原因时返回 `ErrInvalidRemoveFromCRL` 并拒绝整份增量。
5. **过期判定**：基础 CRL 或任一参与合并的增量 CRL 超过 `nextUpdate` 时，
   `Check` 一律返回 `Unknown`，避免在数据可能已陈旧时误报 `Good`。

增量按 `cRLNumber` 升序合并，与到达顺序无关；增量先于基础到达时进入暂存区，
基础到位后自动激活。基础日更后，序号被新基础覆盖的旧增量自动淘汰。

## 测试

`kit_test.go` 用 ECDSA P-256 自建 CA，真实签出带 IDP 与 deltaCRLIndicator 的
基础/增量 CRL 序列。`deltacrl_test.go`、`edge_test.go` 覆盖：

- 基础+增量合并、`certificateHold` 暂停与 `removeFromCRL` 解除；
- 增量乱序到达、先于基础到达的暂存与激活；
- 基础日更后旧增量被淘汰、旧 CRL 重放被拒；
- `baseCRLNumber` 指向未来基础、分发点/签发者不一致、外部 CA 签名、篡改；
- 基础或增量过期返回 `Unknown`、非法解除、超大 CRL 序号（超 int64）。

```bash
go test -race ./...
```
