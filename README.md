# impact — 两版调用摘要的批量撤销分析

`impact` 是给 Go 构建宿主直接导入的影响分析包。它比较两版带签名来源身份的
调用摘要，在至多 20 个构建选项上计算每个入口的：

- `Affected`：存在一条证据完整、在当前配置下可行的调用路径，触及已撤销对象。
- `Unknown`：未被 `Affected` 覆盖，但可行路径上缺少摘要、证书/签名或符号绑定证据，
  因而无法判定。

两者都是与输入 `Valid` 使用同一变量序的规范 ROBDD，并且互不相交。发布服务应撤销
`Affected | Unknown` 中仍有配置的签名产物，即 `Result.MustRevoke()`。

模块路径：`prescreen.local/ddd-1570-20261122-v2`，包名：`impact`。

## 快速开始

```go
manager, _ := impact.NewManager([]string{"fips", "race"}) // 固定变量序，最多 20 个
valid := manager.True()

root := impact.Provenance{
    Module: "example.com/app", Version: "v1.2.3",
    Package: "app", Object: "main.o", Symbol: "main", BuildID: "build-1",
}
dep := impact.Provenance{
    Module: "example.com/lib", Version: "v1.0.0",
    Package: "lib", Object: "lib.o", Symbol: "Run", BuildID: "build-2",
}

ring := impact.NewKeyRing()
_ = ring.Add("release", []byte("shared release key"))
sign := func(s impact.Summary) impact.SignedSummary {
    cert, _ := ring.Sign("release", manager, s)
    return impact.SignedSummary{Summary: s, Certificate: cert}
}

snap, _ := impact.BuildSnapshot(manager, valid, []impact.SummaryKey{root.Key()},
    []impact.SignedSummary{
        sign(impact.Summary{Provenance: root, Body: []impact.Event{{
            Kind: impact.EventCall, Site: "main-run",
            Target: impact.SymbolRef{Name: "Run"},
            Guard: manager.Var("fips"),
        }}}),
        sign(impact.Summary{Provenance: dep}),
    },
    map[string][]impact.Binding{
        "Run": {{Target: dep.Key(), Provenance: dep, Object: dep.ObjectID()}},
    })
snap.SetKeyRing(ring)

engine, _ := impact.NewEngineWithRevocations(snap, dep.ObjectID())
analysis, _ := engine.Analyze()
result, _ := analysis.Result(root.Key())

println(result.Affected.String())   // fips
println(result.Unknown.String())    // 0
println(result.MustRevoke().String())
```

## 摘要语义

一个 `Summary` 表示一个函数的声明式调用摘要，而不是顺序执行的语句块。

- `EventCall`：在 `Guard` 成立时可能调用 `Target` 符号。
- `EventReturn`：该函数可以返回；用于调用/返回配对检查。
- `EventRegister`：在 `Guard` 成立时注册一个回调句柄。`Handle` 在该摘要内唯一标识
  一次注册；`Origin` 绑定句柄的对象来源。省略 `Origin` 时，句柄来源由回调目标绑定
  推导。
- `EventUnregister`：在 `Guard` 成立时注销对应句柄。
- `OptionalSymbol`：同名可选符号的可用条件；多个定义按逻辑或组合。

同一摘要中的调用是候选行为，沿调用链的 guard 会逐层合取。因此 `x` 边和 `!x` 边
不能共同支撑一条路径。递归环使用最小不动点求解，深度不受限制。

回调句柄在“已注册且未注销”的配置中存活：`registered & !unregistered`。某个注册
摘要返回时，存活句柄的回调可能执行；回调完成与注册点配对，不会把效果错误地归到
任意调用者。不同句柄互相独立，注销一个句柄不会影响另一个句柄。注册/注销条件由
BDD 表示。

## 来源身份与证据

- 摘要按完整 `Provenance` 的精确键索引：`Module/Version/Package/Object/Symbol/BuildID`。
- `ObjectID` 撤销时，留空的字段作为通配符；例如省略 `BuildID` 可按对象路径撤销该对象
  的所有构建实例。
- 调用只解析绑定表中的精确目标键。目标摘要缺失时是 `Unknown`，绝不会按同名符号
  借用另一个安全对象的摘要。
- 强绑定优先于弱绑定；只有没有任何强绑定生效时弱绑定才参与选择。两个不同的强目标
  同时生效视为歧义证据，进入 `Unknown`。仅修改弱绑定不会改变强绑定获胜的配置。
- `Certificate` 覆盖来源身份、摘要内容和所有 BDD 的结构键。缺失证书、摘要变化导致
  的摘要不匹配、签名不匹配都会沿实际可达路径传播为 `Unknown`。
- 不可达的缺证摘要不污染入口结果：分析只从入口出发发现可达摘要。

## BDD

- `NewManager(vars)` 固定变量序并拒绝超过 20 个选项。
- 所有 BDD 都是由唯一表维护的规范 ROBDD；相同布尔函数在同一 Manager 中得到相同节点。
- 支持 `And/Or/Not/Xor/Implies/Ite/Restrict/Exists/ForAll/Parse/Enumerate/SatCount`。
- `Analysis` 的所有结果都直接由快照的 Manager 生成，因而与 `Valid` 保持相同变量序，
  可直接做结构相等比较。
- `Manager` 的 BDD 构建不是并发写入安全的；并发分析请为每个 goroutine 使用独立
  Manager，或在外层串行化。

## 增量更新

`Engine` 在克隆后的快照上工作，原 `Snapshot` 和原证书保持不变。分析会记录每个入口
依赖的摘要、符号绑定和对象；更新时只失效依赖命中的入口：

```go
changed, err := engine.ReplaceSummary(newSignedSummary)
engine.RevokeObject(objectID)
engine.RestoreObject(objectID)
changed, err = engine.SetBindings("Run", newBindings)
analysis, err = engine.Analyze()
stats := engine.Stats() // EntriesRecomputed / EntriesReused
```

递归环中的最后一条支撑路径消失时，环内摘要的依赖闭包会被重新计算。新增一个此前
缺失的摘要也会使依赖该目标键的入口失效。

`CompareSnapshots(old, new)` 返回语义 `ChangeSet`，包括修改/新增/删除的摘要、绑定
变化及对象级变化。`NewEngineFromVersions(old, new, revoked)` 是两版输入的便捷入口。

## 独立验收器

`Verifier` 不依赖符号分析的求解过程：

- `VerifySnapshot` 检查来源身份、绑定一致性和调用/返回/回调配对；证书是否有效属于
  证据缺失语义，不会让结构校验失败。
- `VerifyCertificates` 在提供 key ring 时做严格 HMAC 校验；没有 key ring 时校验
  provenance、签名存在性和摘要。
- `VerifyAnalysis` 对 `Valid` 的全部配置逐一做布尔解释，独立求出每个入口的
  `Affected/Unknown`，再与结果 BDD 做精确比较。
- 默认最多枚举 `1 << 20` 个配置；可用 `Verifier.MaxConfigs` 调整。

```go
if err := impact.NewVerifier().VerifyAnalysis(snap, revoked, analysis); err != nil {
    // 结果 BDD、来源身份或配对不一致
}
```

## 测试覆盖

`go test ./...` 包括：

- BDD 规范性、枚举和固定变量序。
- 短调用深度小程序与全配置枚举/独立验收器对照，以及 40 组确定性随机小程序。
- 深度不限的互递归固定点。
- 不同句柄的重复注册与注销。
- 不能同时成立的条件边。
- 根摘要缺失、仅弱绑定变化、同名安全对象不借用。
- 摘要替换、撤销/恢复对象、绑定变化、缺失摘要补齐、递归环最后支撑路径消失的增量更新。
- 原始快照证书在 Engine 更新后仍可核验。

## 主要 API

| 类型/函数 | 用途 |
| --- | --- |
| `Manager`, `BDD` | 固定序规范 BDD |
| `Provenance`, `ObjectID` | 精确来源/对象身份 |
| `Summary`, `Event`, `OptionalSymbol` | 调用摘要 |
| `KeyRing`, `Certificate`, `SignedSummary` | 签名与证据 |
| `Snapshot`, `BuildSnapshot` | 一个版本的完整输入 |
| `CompareSnapshots`, `ChangeSet` | 两版语义差异 |
| `Engine`, `Result`, `Analysis` | 增量分析结果与撤销决策 |
| `Verifier` | 独立全配置验收 |
