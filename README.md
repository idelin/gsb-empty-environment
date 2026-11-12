# 文件解密策略的冲突不能只报告布尔公式无解

文件解密策略的冲突不能只报告布尔公式无解。写供Go发布服务调用的策略诊断包，主体属性、文件密级与操作均为有限域，规则含允许、拒绝及必须双人批准；拒绝优先，双人批准要求两个不同公钥身份。把业务可用性要求与禁止越权要求共同展开，返回最小冲突规则组，并为移除任一组给出具体主体、文件和批准者见证。最多18组规则，预算不足明确未最小；用有限域全枚举核对不能靠同一人的两个别名满足双人批准。

## policydiag

`policydiag` 是供 Go 发布服务在策略发布前调用的诊断包，入口为
`policydiag.Diagnose(policy, options)`，另有 `policydiag.Evaluate` 与
`policydiag.EligibleApproverPairs` 用于单请求解释。

- 有限域：操作、主体公钥身份（含 `PersonID` 别名关系）、角色、密级、文件密级全部显式枚举。
- 规则三类：`allow`、`deny`、`require_dual`；拒绝优先于允许。
- 双人批准：两名批准者必须是不同 `PersonID` 的公钥身份，actor 本人及其同人名别名都被排除。
- 需求两类：`availability`（业务可用）与 `no_unauthorized`（禁止越权），共同展开到完整笛卡尔积。
- 冲突组即最小修正集：删除组内全部规则后所有需求同时成立，任一真子集都仍有违反；每组带具体主体、文件和批准者见证。
- 上限 18 条规则；`Options.MaxChecks` 控制预算，预算耗尽时 `Report.BudgetExhausted=true` 且各组 `Minimal=false`。
- `Report.AliasAudit` 用有限域全枚举核对：同一人的两把钥匙永远不能凑成双人批准。

运行测试：`go test ./...`
