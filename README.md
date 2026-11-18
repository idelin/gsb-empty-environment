# crate审计增量链与违规区间

依赖门禁用审计记录判断 crate 版本能否部署，攻击者发布的版本落在 violation 区间，或借 delta 审计链接到一个未被完整审计的起点。为Go门禁实现可导入的审计链解析包：完整审计给出 version，增量审计给出 from->to；一个版本通过当且仅当存在从某完整审计出发、沿同一 criteria 的增量边可达它的路径；violation 的 semver 区间内的任何版本一律不通过，且 violation 优先于审计；safe-to-deploy 蕴含 safe-to-run，反向不成立；预发布版本按 semver 排序，+ 构建元数据忽略。用环、断链、区间边界和 criteria 蕴含验证。

## 包结构

`netsec` 包仅依赖标准库，离线可构建：

- `version.go`：`ParseVersion` / `Version.Compare`，严格 semver 解析（可选 `v` 前缀），实现预发布标识符排序（数值按数值、字母按 ASCII、短集排在长集之前、无预发布高于有预发布），`+` 构建元数据参与校验但不参与优先级与规范化输出，含前导零与溢出检查。
- `range.go`：`VersionRange` 与 `ParseVersionRange`，区间语法 `[1.0.0,2.0.0)`，方括号包含、圆括号排除，空边表示无界；`Contains` 严格按边界判定，故发布版本区间不含其预发布版本。
- `resolver.go`：`FullAudit`、`DeltaAudit`、`Violation` 记录与 `Resolver`。

## 判定语义

- `NewResolver` 校验并规范化所有记录，按 criteria 分组，从每个完整审计出发做 BFS；`visited` 标记保证环安全，起点不可达的 delta 边构成断链且不产生任何可达版本。
- `Resolver.Allowed(version, criteria)` 先查 violation 区间（命中即拒绝，violation 优先于审计），再在蕴含该 gate criteria 的审计 criteria 可达集中查找；路径必须始终沿同一 criteria。
- 内置蕴含 `DefaultCriterionImplicate`：恒等蕴含，且 `safe-to-deploy` 蕴含 `safe-to-run`，反向不成立；可传入自定义 `CriterionImplicate` 扩展层级。
- `Resolver.Violated` 单独查询某版本是否被违规区间覆盖。

## 验证

`go test ./...` 覆盖环（含自环与回边）、断链（delta 接到未审计起点）、区间开闭边界与预发布边界、criteria 蕴含正反向、构建元数据等价性、违规覆盖完整审计根等场景，并通过 `-race` 检测。
