# 依赖源迁移批准范围

企业将私有包源迁到新域，批准不能顺带允许同名公共包。写供Go安装宿主调用的迁移映射包，规则绑定旧源身份、包名范围、版本区间及新源证书指纹；多个规则重叠时必须指向同一目标，否则返回具体冲突版本。解析在固定规则快照下执行，用有限版本域枚举验证边界、撤销及无规则时不得回退。

## 实现

`netsec` 包（无第三方依赖）提供固定快照式的迁移映射：

- `Rule` 四要素绑定：`SourceIdentity`（旧源主机名 + 可选实例 ID）、
  `PackageScope`（精确路径或以 `/` 结尾的段级前缀）、`VersionRange`
  （显式半开/闭区间，拒绝通配符）、`Target`（新源主机 + SHA-256 证书指纹）。
- `Compile(id, rules, domain)` 在有限 `VersionDomain` 上逐模块、逐具体版本枚举，
  检查区间边界与规则重叠；重叠规则必须收敛到同一 `Target`，否则返回
  `ConflictError`，其中每条 `Conflict` 给出具体冲突版本及涉及的规则 ID。
  显式撤销（`Rule.Revoked`）与批准重叠同样判定为冲突；命中不到域内任何版本的
  规则直接拒绝编译。
- `Snapshot.Resolve` / `ResolveModule` 只在不可变快照上解析。成功的唯一结果是
  带证书指纹的私有目标；无规则、已撤销、版本或模块不在域内分别返回
  `ErrNoRule`、`ErrRevoked`、`ErrUnknownVersion`、`ErrUnknownModule`，
  宿主应据此拒绝抓取且不得回退到公共代理。

## 宿主调用示例

```go
rng, err := netsec.ParseVersionRange(">=1.0.0 <2.0.0")
if err != nil { /* 区间非法 */ }

domain := netsec.NewVersionDomain()
_ = domain.Add("git.example.net/team/lib", "1.0.0", "1.5.0", "2.0.0")

fp, _ := netsec.ParseFingerprint("ab:cd:...（64 位十六进制）")
snap, err := netsec.Compile("snap-2026-11", []netsec.Rule{{
	ID:        "r1",
	OldSource: netsec.SourceIdentity{Host: "git.example.net"},
	Scope:     netsec.PackageScope{Prefix: "git.example.net/team/lib"},
	Versions:  rng,
	Target:    netsec.Target{NewHost: "modules.example.corp", Fingerprint: fp},
}}, domain)
if err != nil { /* 冲突或规则非法，启动失败即拒绝一切安装 */ }

target, err := snap.ResolveModule(
	netsec.SourceIdentity{Host: requestedHost},
	modulePath, requestedVersion,
)
if err != nil {
	// ErrNoRule / ErrRevoked / ErrUnknownVersion / ErrUnknownModule：
	// 失败关闭，禁止回退公共源。
}
// 仅允许连接 target.NewHost 并校验 target.Fingerprint。
```
