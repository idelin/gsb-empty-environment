云原生策略引擎在求值声明式访问控制规则（类 Rego Datalog 子集）时，若规则缺少 `default allow := false` 导致未匹配时返回 `undefined` 被外层误判为放行、存在不安全（unsafe）未绑定变量，或否定（`not`）条件中局部变量作用域泄漏，策略更新后可能意外向全网开放敏感接口。为 Go 策略安全宿主实现可导入的声明式授权规则安全编译器与静态越权验证包。组件解析规则集（含事实表、`allow`/`deny` 规则体、局部否定 `not` 与分层依赖），静态强制：（1）所有变量必须受限于正原子（Safety Check），拒绝不安全未绑定变量与非分层递归否定环；（2）Fail-Closed 语义保证任何 `undefined` 或多值冲突（`allow` 与 `deny` 同真）一律收紧为拒绝；（3）内置有界反例搜索器，对照宿主声明的“绝对禁止放行场景”（如匿名主体写操作、跨租户访问）自动搜索可使 `allow=true` 成立的最小输入事实集。附不安全变量、否定环与冲突默认拒绝测试。

## policysafe

可导入包 `policysafe`（模块路径 `formal.local/w27-6f5ae83635/policysafe`）实现上述安全编译器与静态越权验证器。

### 语言子集

```rego
# 事实表（必须完全实例化）
role(alice, admin).
# 规则体支持正原子、局部否定 not 与正向递归
allow(Subj) :- role(Subj, admin), not blocked(Subj).
# 显式 fail-closed 默认（缺失时编译器同样按 fail-closed 处理）
default allow := false.
```

### 静态保证

- **Safety Check**：规则中每个具名变量都必须出现在某个正原子中；仅出现在 `not` 内的变量、未绑定的头部变量、非实例化事实与头部通配符一律拒绝。
- **分层（Stratification）**：Tarjan 强连通分量分析拒绝跨越否定边的递归环（含 `p :- not p`），正向递归（如传递闭包）保持合法；通过后按谓词分层做 bottom-up 不动点求值。
- **Fail-Closed**：`Compiled.Authorize` 将 `undefined`、求值错误与 `allow`/`deny` 同真冲突全部收紧为 `deny`，仅在 allow 命中且 deny 未命中时返回 `allow`（原因码 `allow_matched`/`undefined_denied`/`explicit_deny`/`conflict_denied`）。
- **有界反例搜索**：`FindCounterexample`/`Verify` 按候选事实子集大小迭代加深，返回使禁止场景的 `allow` 目标可推导的**基数最小**事实集；受 `MaxFacts`/`MaxEvaluations` 双重预算约束，超预算返回 `ErrSearchLimit`，并禁止向派生谓词注入“事实”。

### 用法

```go
compiled, err := policysafe.Compile(src)
decision := compiled.Authorize(requestFacts) // 永远 fail-closed

violations, err := compiled.Verify([]policysafe.Scenario{{
    Name:           "anonymous-subject-write",
    Goal:           policysafe.AtomOf("allow", policysafe.V("Subj"), policysafe.C("write")),
    FixedFacts:     []policysafe.Atom{policysafe.AtomOf("anon", policysafe.C("attacker"))},
    CandidateFacts: attackerControllableFacts,
}}, policysafe.SearchOptions{})
```

### 测试

`go test ./...` 覆盖不安全变量、非实例化事实、非分层否定环、正向递归、否定局部作用域、undefined/显式/冲突拒绝、匿名写与跨租户反例、搜索预算与派生谓词注入拦截。
