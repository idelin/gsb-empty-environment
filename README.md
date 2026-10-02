# leasectl — 支持动态续期与平滑优雅移交的租约控制器

纯 Go 标准库实现,无第三方依赖。

## 组件

- **`lease.Coordinator`** — 协调中心,持有租约状态,负责获取、动态续期、
  移交准备/完成/中止、写入校验(持有者 + 防护令牌 term + 过期 + 冻结态)。
- **`lease.Node`** — 服务节点,持有本地租约凭证,提供 `Write`(业务写入)、
  `Renew`(动态续期)、`InitiateHandover`(主动移交)、`AcceptHandover`(接管)。
- **`lease.Router`** — 业务侧路由器,把写入发往当前主节点;移交瞬间自动
  重试到新主,调用方无感。

## 主动移交(Handover)协议

1. 持有者 A 计划停机维护,先**冻结本地写入**(冻结期间的写入短暂阻塞而非报错);
2. A 向协调中心发出主动释放指令 `PrepareHandover`,指定接盘候选人 B,
   协调中心冻结租约并签发**交接包**(新防护令牌、剩余 TTL、租约上下文快照);
3. B 收到交接包后 `CompleteHandover` **原子切换**持有者,继承剩余租约上下文,
   防护令牌 +1 使旧凭证立即失效(防脑裂);
4. A 清除本地凭证并解冻;若候选人接管失败,`AbortHandover` 让 A 原样恢复。

## 运行

```sh
go test ./lease/ -v        # 单元测试 + 验收场景
go test ./lease/ -race     # 竞态检测
go run ./cmd/demo          # 运行样例:滚动重启下的零错误移交
```

## 验收场景对应

`lease/node_test.go` 中的 `TestGracefulHandoverZeroDowntime`:
主节点 A 收到滚动重启指令 → 冻结本地写入 → 向候选 B 移交租约凭证 →
B 立即无缝接管主角色;全程并发业务写入零错误,切换耗时微秒级(断言 < 10ms)。
`cmd/demo` 以真实时钟演示同一流程。
