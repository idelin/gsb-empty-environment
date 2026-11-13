# 物化视图刷新绕过当前行策略

共享物化视图由高权限所有者刷新，低权限查询者若直接读取完整缓存，会绕过当前行级策略。给Go数据库宿主实现可导入的安全物化视图包：视图定义含源对象ID/版本、投影表达式、ownerID、刷新模式{OwnerSnapshot,PerPolicyPartition}和定义摘要。OwnerSnapshot只允许存储不含租户数据的公共聚合，调用方必须提供静态信息流摘要证明每个输出不依赖受保护列；摘要含保护列、可能抛错或有副作用时证明无效；摘要含保护列、可能抛错或有副作用时证明无效；证明缺失则拒绝创建。PerPolicyPartition按(policyDigest,由角色闭包摘要确定的由角色闭包摘要确定的subjectClass)分别刷新，刷新计划在固定策略快照下对每行先求RLS再投影，缓存条目绑定源版本、策略epoch、由角色闭包摘要确定的由角色闭包摘要确定的subjectClass和刷新事务LSN。查询者只能命中其当前由角色闭包摘要确定的由角色闭包摘要确定的subjectClass和policyDigest条目，执行前重验策略epoch；策略或角色变化使旧分区不可用。增量刷新事件若一行从可见变不可见必须删除其贡献，聚合使用可撤回单子；无法撤回的聚合退回全量刷新。并发刷新在新条目完整后原子替换，失败保留旧条目但若epoch已变仍不可读。实现待审缓存键差分搜索和小表逐行参考，覆盖owner可见/用户不可见、角色迁移、RLS收紧、SUM撤回、源表同名重建及刷新中断。

## 实现

包 `netsec`（module `example.org/netsec/q0121`，仅依赖标准库）在进程内宿主上实现
上述安全物化视图，关键文件：

- `view.go` / `flow.go` / `expr.go` / `func.go`：视图定义、投影表达式与静态信息流证明。
- `policy.go`：角色闭包、subjectClass 摘要、RLS 策略与策略 epoch。
- `cache.go` / `refresh.go` / `incremental.go`：分区缓存、全量/增量刷新、可撤回单子与原子替换。
- `query.go`：查询期对源版本、策略 epoch、subjectClass、policyDigest、LSN 的重验。
- `pending.go`：待审缓存与缓存键差分搜索（missing/extra/value）。
- `reference.go`：小表逐行 RLS→投影参考引擎及一致性校验。

## 验证

```
go test -race -count=1 ./...
```

外部测试包 `netsec_test` 覆盖：owner 可见/低权限用户不可见且分区隔离、角色迁移、
RLS 收紧、SUM 可见→不可见撤回（含插入/删除路径）、MIN 不可撤回退回全量、
源表同名重建、刷新中断保留旧条目且暂存条目不可查询、OwnerSnapshot 证明拒绝
（保护列/抛错/副作用/缺失/篡改/裸行）、待审差分与审批、并发刷新无撕裂读。
