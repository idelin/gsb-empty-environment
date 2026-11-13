# 撤销集合的 RSA 累加器证明

包 `netsec` 是可由 Go 服务直接导入的固定代数实现。宿主提供可信奇数
`N`、单位群元素 `g`、集合 ID、代次及互异的 32 位素数集合。集合乘积为
`P`，根为 `A = g^P mod N`。

```go
params, _ := netsec.NewParams(n, g)
acc, err := netsec.New(params.N, params.G, "revocation-list", 1, []uint32{3, 5, 7})

memberProof, err := acc.ProveMembership(5)
if err := acc.VerifyMembershipFor(5, memberProof); err != nil {
	// reject
}

nonMemberProof, err := acc.ProveNonMembership(11)
if err := acc.VerifyNonMembershipFor(11, nonMemberProof); err != nil {
	// reject
}
```

成员证明计算 `W = g^(P/x)` 并验证 `W^x = A`。非成员证明计算满足
`a*x + b*P = 1` 的整数系数，保留 `0 <= b < x`，处理负的 `a` 时对 `g`
取模逆，给 `D = g^a` 并验证 `D^x*A^b = g`。空集合的 `P = 1`，集合最多
128 项。

`Add` 和 `Delete` 会从当前完整集合重算乘积和新根，并把代次加一。旧根、
旧代次或旧集合 ID 的证明会被拒绝；即使调用方把证明中的身份字段改成新值，
旧见证也无法满足新一代方程。需要保留旧快照时先用 `Clone`。

## 身份与验证边界

- 证明携带 `(SetID, Generation, RootDigest)`，但验证根始终来自宿主参数或
  宿主传入的 `root`，绝不会从证明中取根。
- `VerifyMembershipAt` / `VerifyNonMembershipAt` 从宿主给出的根、集合 ID
  和代次重新计算可信身份摘要。
- 身份字段是严格的相等性绑定，不是签名。部署方必须从可信宿主状态得到
  期望身份，不能把证明中的身份当作可信输入。
- 一次验证只说明该证明对给定参数和该 `(集合 ID, 代次, 根摘要)` 快照满足
  本题代数与身份条件。它不是可缓存的“当前撤销结论”；增删后必须重新验证。
- 包不声称任何 `N` 具备抗分解性。测试中的 `N = 3233` 只用于参考核对，
  不是部署配置。

运行检查：

```sh
go test ./...
go vet ./...
```
