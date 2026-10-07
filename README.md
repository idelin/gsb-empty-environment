# verifysched

验证调度库：将一组检查冻结在同一个发布包（bundle）中，并对每次运行做绑定校验。

## 核心模型

- **Freeze**：以输入摘要 + 检查集合（验证器及版本）冻结一个 bundle；冻结时旧 bundle 连同其结果自动归档，仅用于审计，永不再计入新门禁。
- **Dispatch**：为当前 bundle 中未完成的检查签发运行 id（`bundle-N/check#attempt`）；出错的检查会以全新运行 id 重新签发，旧 id 立即失效。
- **Submit**：回执可乱序到达，但 bundle id、运行 id、输入摘要、验证器版本四项绑定必须全部匹配当前冻结包，否则拒绝（归档 bundle 的回执只归档并返回 `ErrStaleBundle`）。
- **Revoke**：撤销当前 bundle 中的检查，将其从门禁中移除；撤销后的回执被拒绝。
- **Gate**：仅评估当前冻结包——所有未撤销检查均有通过结果才放行；待回执 / 失败 / 出错 / 撤销分别列出。

## 错误语义

`ErrStaleBundle` / `ErrStaleInput` / `ErrStaleValidator`：输入或规则变化后的过期结果；
`ErrRunMismatch`：复用旧运行 id；`ErrDuplicateResult`：重复提交；`ErrRevokedCheck`：已撤销检查。

## 测试

`go test ./...` 覆盖：乱序回执、部分通过、撤销检查、输入/规则变化后旧结果仅归档、出错重发与错误复用。
