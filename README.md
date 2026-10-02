# temporalrbac — 时态权限委托与继承解析器

带时态约束的 RBAC 权限委托内核（Go，仅标准库）。

## 模型

双时态（bitemporal）：

- **业务时间 (valid time)**：授权 / 委托 / 收回在业务上的生效区间 `[From, To)`；
- **知悉时间 (audit time)**：系统记录该事实的时间。

查询 `HoldsRole(user, role, bt, kt)` / `EffectivePermissions(user, bt, kt)` 回答：
“在知悉时刻 `kt` 看来，用户在业务时刻 `bt` 是否实际持有该角色 / 哪些权限”。

一条记录（授权或委托）在 `(bt, kt)` 下有效，当且仅当：

1. `RecordedAt <= kt`（记录已知悉）；
2. `bt ∈ [Valid.From, Valid.To)`（业务区间内）；
3. 不存在 `RecordedAt <= kt` 且 `EffectiveAt <= bt` 的收回记录。

委托链递归解析：任一上游环节过期或被收回，下游在同一时刻随之失效
（级联失效）。委托创建时校验委托人在委托起始业务时刻实际持有该角色；
委托环通过访问标记拒绝，不会死循环。

## API

- `DefineRole(role, perms...)` — 定义角色及权限
- `GrantRole(user, role, valid, recordedAt)` — 直接授权（链的源头）
- `Delegate(delegator, delegatee, role, valid, recordedAt)` — 临时委托
- `Revoke(targetID, effectiveAt, knownAt)` — 提前收回
- `HoldsRole(user, role, bt, kt)` / `EffectivePermissions(user, bt, kt)` — 查询

## 运行

```sh
go test ./... -v   # 测试（含验收场景）
go run .           # 运行样例：A->B->C 委托链 + 提前收回
```
