# config-lease-binder

带版本约束的配置信息租约绑定器 —— 集群动态配置分发与生效机制。

## 机制

- 工作节点向配置中心**租用**某个版本的配置，获得一张带 TTL 的**租约凭证**；
- 配置更新时，配置中心将版本号 +1、**撤销全部旧版本租约**，并向在线节点**推送新配置与新租约**；
- 节点**只在持有有效租约时对外接客**（`serve` 否则抛 `NoValidLeaseError`）；
- 脱网节点收不到推送也无法续租，其旧租约在 **TTL 到期后自然失效**，节点主动停止接收流量，
  防止使用过期配置操作脏数据；
- 版本约束是硬保证：旧版本租约在中心侧立即作废（`version-mismatch`），
  重连节点续租被拒后会立即停服，不必等 TTL。

## 结构

- `src/lease-binder.ts` — 租约绑定器：签发 / 校验（版本约束 + TTL + 撤销）/ 续租 / 批量撤销
- `src/config-center.ts` — 配置中心：持有当前配置，发布变更并推送新租约
- `src/worker-node.ts` — 工作节点：租用、续租、持有效租约才接客、脱网自然失效停服
- `src/clock.ts` — 可注入时钟（测试用 `ManualClock` 确定性推进时间）
- `test/lease-binding.test.ts` — Node.js 内置 `node:test` 测试（10 个用例）
- `examples/demo.ts` — 端到端运行样例

## 运行

要求 Node.js >= 22.6（使用 `--experimental-strip-types` 直接运行 TypeScript，零依赖）。

```bash
npm test    # 运行测试
npm run demo  # 运行演示
```

## 验收场景覆盖

1. **配置中心修改数据库密码** → `ConfigCenter.updateConfig({ dbPassword: "pw-v2" })`；
2. **发布新配置并收回旧租约** → 版本 +1、`revokeVersion` 撤销旧租约、在线节点收到新配置与新租约；
3. **脱网慢节点旧租约自然失效主动停流** → `disconnect()` 后收不到推送，TTL 到期后
   `maintain()` 使其停止接客，`serve` 抛错。
