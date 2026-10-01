# temporal-rbac：时态权限委托与继承解析器

带双时态约束的 RBAC 权限委托内核，纯 Go 标准库实现。

## 模型

- **授权 Grant**：授予者将角色授予被授予者，业务时间区间 `[ValidFrom, ValidTo)`
  内有效，自知悉时间 `RecordedAt` 起对审计可见。`Grantor == ""` 表示系统
  直接授权（委托链的根）。
- **撤回 Revocation**：在知悉时间 `RecordedAt` 记录，使目标授权自业务时间
  `ValidFrom` 起失效（提前收回，可追溯）。
- **查询**：`EffectivePermissions(user, vt, tt)` 返回用户在业务时间 `vt`、
  审计时间 `tt` 下的实际有效权限集合。授权 `g` 有效当且仅当：
  - `g.RecordedAt <= tt`（审计时已知悉该授权）
  - `g.ValidFrom <= vt < min(g.ValidTo, 所有已知撤回的 ValidFrom)`
- **委托链**：非系统授权仅在授予者本人于同一 `(vt, tt)` 下实际持有该角色
  时有效；递归解析，上层过期/撤回沿链传播，含委托环保护。

## 目录

- `temporal.go` — 解析内核（`Resolver`、`Grant`、`Revocation`）
- `temporal_test.go` — `go test` 测试，含验收边界
- `cmd/demo/main.go` — 可运行样例

## 运行

```sh
go test -v ./...   # 运行测试
go run ./cmd/demo  # 运行样例
```

## 验收场景

系统直授 A 角色 R；A 委托 R 给 B `[10,50)`；B 在 20 委托给 C `[20,60)`；
A 在知悉 30 撤回对 B 的委托、自业务 25 起生效。知悉 35 查询 C 在业务 28
的权限：B 的授权在 `(28,35)` 下已失效，沿链传播，C 判定无权限。

样例输出（`go run ./cmd/demo`）：

```
用户C 业务时间=28 审计时间=25 -> 权限=[doc:read doc:write]  (撤回未知悉（审计25<30），C 在业务28)
用户C 业务时间=28 审计时间=35 -> 权限=[<无>]  (验收边界：知悉35查业务28，应已失效)
用户C 业务时间=24 审计时间=35 -> 权限=[doc:read doc:write]  (撤回自业务25生效，业务24仍有效)
用户C 业务时间=55 审计时间=25 -> 权限=[<无>]  (上游 A->B 业务50过期，C 在业务55失效)
用户B 业务时间=30 审计时间=35 -> 权限=[<无>]  (B 自身在业务30，已被撤回)
用户A 业务时间=30 审计时间=35 -> 权限=[doc:read doc:write]  (A 为系统直授，不受影响)
```

## 测试结果

```
=== RUN   TestAcceptance_CRevokedAtAudit35        --- PASS  （验收边界）
=== RUN   TestRevocationNotYetKnown               --- PASS  （撤回未知悉时仍有效）
=== RUN   TestRevocationEffectiveFromValidTime    --- PASS  （撤回自业务25起生效，左闭右开）
=== RUN   TestChainExpiryPropagates               --- PASS  （多层链上游过期传播）
=== RUN   TestDelegationNotYetStarted             --- PASS  （委托下界未生效）
=== RUN   TestGrantNotYetRecorded                 --- PASS  （授权知悉时间过滤）
=== RUN   TestDelegationWithoutHolding            --- PASS  （无权委托不生效）
=== RUN   TestDelegationCycleTerminates           --- PASS  （委托环终止且无效）
=== RUN   TestThreeLevelChainMiddleRevoked        --- PASS  （三层链中间层撤回连带失效）
PASS
ok  	temporal-rbac
```
