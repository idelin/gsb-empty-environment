# 跨区域安全策略发布

安全策略必须同时安装到多个区域，部分区域失联时不能把全局状态标为已生效。写供Go控制宿主调用的发布组件，版本绑定区域集合与策略摘要，各区先准备再提交，准备失败全局撤回；提交阶段失败只能报告部分生效并持续收敛，不能宣称已回滚。用可控区域代理验证重复确认、旧版回执和控制器重启。

## 组件说明（release 包）

- `Version` 绑定版本号、策略摘要与区域集合；`Receipt` 回执必须匹配版本号与摘要，否则按旧版回执拒绝。
- 两阶段编排：全部区域 `Prepare` 成功后才进入 `Commit`；准备阶段任一失败即全局 `Abort`，状态为 `aborted`。
- 提交阶段失败只报告 `partially_effective`（含已生效/待收敛区域清单），宿主周期调用 `Converge` 直至 `effective`；进入提交阶段后绝不回滚。
- 状态经 `Store`（`MemStore`/`FileStore`，原子写入）持久化；进程重启后调用 `Recover`：未进入提交阶段安全撤回，已进入则继续向前收敛。
- 重复确认由区域集合去重幂等吸收；已提交区域在收敛与重启恢复中不会被重复提交。

## 用法

```go
store := release.NewFileStore("/var/lib/controller/release-state.json")
ctl := release.NewController(store, agents) // agents: map[区域名]release.Agent

rep, err := ctl.Release(ctx, release.Version{ID: "v7", Digest: digest, Regions: regions})
for rep.Partial() { // 部分生效：持续收敛，不得宣称已回滚
    time.Sleep(backoff)
    rep, err = ctl.Converge(ctx)
}
```

控制器重启后：

```go
rep, err := release.NewController(store, agents).Recover(ctx)
// rep.Phase == aborted              -> 中断于提交前，已安全撤回
// rep.Phase == partially_effective  -> 继续调用 Converge 收敛
```

## 测试

`go test ./release/` 使用可控区域代理覆盖：全部生效、准备失败全局撤回、提交失败部分生效并收敛、重复确认幂等、旧版回执拒绝、控制器在准备/提交阶段重启恢复。
