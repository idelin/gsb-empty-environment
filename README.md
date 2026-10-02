# leasefed — 跨集群租约联邦与全局互斥控制器

主备跨数据中心(跨机房)全局活跃租约控制器。通过部署在第三方机房的轻量级
**法官(Witness)** 维持全局单点写入租约;主机房整体掉电时,法官在租约过期后
向备用机房颁发晋升租约,配合 **fencing token** 在存储层彻底杜绝双机房双写。

仅使用 Go 标准库。

## 架构

```
   机房1 (dc1)          第三方机房            机房2 (dc2)
 ┌────────────┐      ┌──────────┐      ┌────────────┐
 │ Controller │─────▶│ Witness  │◀─────│ Controller │
 │ (主, 可写) │ 续租  │ (法官)   │ 竞选  │ (备, 只读) │
 └─────┬──────┘      └──────────┘      └─────┬──────┘
       │ 写(携带 fencing token=epoch)        │
       ▼                                     ▼
 ┌──────────────────────────────────────────────────┐
 │        全局存储层 store.Store(校验 fencing token) │
 └──────────────────────────────────────────────────┘
```

- `witness/` — 法官:全局唯一租约仲裁者。任意时刻只有一份有效租约;
  每次颁发递增 `Epoch`(fencing token)。提供进程内 `Core` 与 HTTP 两种形态
  (`GET /acquire`、`GET /renew`、`GET /status`)。
- `controller/` — 每机房的租约控制器,状态机 `Standby(只读) ⇄ Primary(可写)`。
  备机周期竞选;主机周期续租。续租被法官**明确拒绝**(凭证失效)立即降级;
  网络故障且本地租约已过期时**宁可降级也绝不双写**。
- `store/` — KV 存储,记录已见最大 epoch,拒绝一切携带过时 token 的写入,
  是杜绝双写的最后一道闸。

## 故障转移时序(验收边界)

1. 机房1 主、机房2 备,机房1 持 `epoch=1` 租约正常写入。
2. 光纤挖断,两机房失联,机房1 整体掉电。
3. 法官侧租约 TTL 到期 = 确认机房1 真正失联,向机房2 颁发 `epoch=2` 晋升租约。
4. 机房1 掉电前的迟到写入带 `epoch=1` 到达存储层,被 fencing token 校验拒绝。
5. 机房1 恢复供电,用旧凭证续租被法官拒绝,自动切换为只读备机。

## 运行样例

```sh
go run ./cmd/demo
```

输出(节选):

```
[dc1] 角色变更: STANDBY(只读) -> PRIMARY(可写)
[dc1] 写入 "order-1"="created-by-dc1" 成功 (fencing token epoch=1)
=== 阶段 2: 光纤挖断! 机房1 整体掉电, 与法官失联 ===
[dc2] 角色变更: STANDBY(只读) -> PRIMARY(可写)
[dc2] 写入 "order-2"="created-by-dc2" 成功 (fencing token epoch=2)
[storage] 拒绝机房1 的迟到写入: fencing token 已过时: epoch=1, 存储层已见 epoch=2
=== 阶段 4: 机房1 恢复供电, 用旧凭证尝试续租 ===
[dc1] 角色变更: PRIMARY(可写) -> STANDBY(只读)
[dc1] 最终角色: STANDBY(只读) (降级次数=1)
```

## 测试

```sh
go test ./...
```

- `witness/` — 单点租约、过期接管、续租鉴权、epoch 单调性
- `controller/` — 竞选/续租/降级状态机、瞬时故障容忍、失联安全降级
- `store/` — fencing token 闸门
- `scenario/` — 端到端验收测试(完整走一遍上述故障转移时序)

> 在禁止监听端口的沙箱环境中,HTTP 传输层测试会自动跳过;
> 正常环境下 `controller.TestHTTPTransport` 会走真实 HTTP 往返。
