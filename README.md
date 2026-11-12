# Gradle校验元数据绕过检测

构建网关用 verification-metadata.xml 校验依赖，攻击者利用过宽的 trusted-artifacts 正则、未转义的点号，或让动态版本解析出锁文件之外的版本。为Go网关实现可导入的校验判定包：解析 components 的 sha256 与 trusted-artifacts 的 group、name、version、file 正则（按整串匹配，regex=true 才是正则，否则字面），把 .* 或匹配全部 group 的规则标为风险；解析结果里存在不在 lockfile 的版本，或 1.+ 之类动态版本未被锁文件覆盖时给出绕过告警；also-trust 备用校验和只在列出的算法内有效。用宽正则、点号转义、动态版本和多校验和验证。

## 包结构

`netsec` 是可导入的判定包，仅依赖 Go 标准库：

- `parser.go`：解析 `verification-metadata.xml`（components、artifacts、主校验和、also-trust、trusted-artifacts）与 Gradle lockfile。
- `rules.go`：规则匹配（regex=true 时整串锚定，否则字面匹配）、`.*` 宽正则识别、未转义点号检测、Gradle 动态版本与版本区间求值。
- `analyze.go`：产出风险/绕过告警（宽正则、未转义点号、非法正则、lockfile 未覆盖的动态版本、lockfile 之外的 component 版本、未知校验算法）。
- `verify.go`：`Metadata.Verify` 判定入口，先比对主校验和，再按 also-trust 白名单接受备用算法，最后才允许 trusted-artifacts 规则兜底，并把规则风险附在决策上。

## 判定语义

- 正则规则统一用 `^(?:pattern)$` 锚定，杜绝子串匹配；非法正则记为风险且永不匹配。
- `.*`、`.+`、`^.*$`、`(.*)`、`.*|.*` 等匹配任意值的规则标记 `overbroad-regex`。
- 未转义且不在字符类内的 `.` 标记 `unescaped-dot`（`com.evil` 也能匹配 `comXevil`）。
- 字面规则只有值完全相等才匹配；`1.+`、`+`、`latest.release`、`[1.0,2.0)` 按 Gradle 动态选择器求值，lockfile 中没有任何坐标可覆盖该规则时发出 `uncovered-dynamic-version` 告警。
- component 版本在 lockfile 中没有精确坐标时发出 `version-not-in-lockfile` 告警。
- also-trust 校验和仅当其算法出现在 metadata 的 also-trust 启用列表中才有效；未列出的算法拒绝并给出 `unsupported-algorithm`。

## 快速使用

```go
meta, err := netsec.ParseMetadataBytes(xmlData)
lock, err := netsec.ParseLockfileBytes(lockData)

for _, finding := range meta.Analyze(lock) {
    log.Printf("%s: %s", finding.Severity, finding.ID)
}

decision := meta.Verify(netsec.ArtifactRef{
    Group: "com.example", Name: "safe-lib", Version: "1.0", File: "safe-lib-1.0.jar",
}, map[string]string{"sha256": computedSHA256})
if !decision.Allowed {
    return errors.New(decision.Reason)
}
```

## 测试

```sh
go test ./... -race -cover
```

测试覆盖宽正则与匹配全部 group、点号转义与字符类、整串锚定语义、`1.+`/版本区间动态版本、lockfile 覆盖告警、also-trust 多校验和（含未列出算法拒绝）以及 trusted-artifacts 兜底决策。
