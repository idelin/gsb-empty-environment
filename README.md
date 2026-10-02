# configlease — 带版本约束的配置信息租约绑定器

集群动态配置分发与生效机制（Go 标准库实现，无第三方依赖）：

- **配置中心（`Center`）**：持有当前配置（版本号 + 数据库密码等），签发、续期、撤销租约。
- **租约（`Lease`）**：绑定具体配置版本的限时凭证（ID、版本、到期时间）。
- **工作节点（`Node`）**：只有持有未过期租约时才对外接客；租约一旦自然过期，节点**本地自我隔离**（fencing），拒绝流量，避免用过期配置操作脏数据。

## 核心流程

1. 节点 `Acquire`/`Renew` 向中心租用配置版本，获得 `(Config, Lease)`。
2. 中心 `UpdateConfig` 修改配置：版本号 +1，**撤销全部在途旧租约**。
3. 节点续期时若旧租约已撤销/过期/版本落后，中心直接下发**新配置 + 新租约凭证**，节点原子切换。
4. 脱网节点无法续期 → 旧租约 TTL 到期 → `Serve` 返回 `ErrLeaseExpired`，停止接收流量。

## 运行

```sh
go test ./... -v     # 测试
go run ./cmd/demo    # 运行样例（真实时钟演示完整验收场景）
```

## 测试结果

```
=== RUN   TestNodeServesWithValidLease
--- PASS: TestNodeServesWithValidLease (0.00s)
=== RUN   TestConfigUpdateRevokesOldLeases
--- PASS: TestConfigUpdateRevokesOldLeases (0.00s)
=== RUN   TestDisconnectedNodeFencesItselfOnLeaseExpiry
--- PASS: TestDisconnectedNodeFencesItselfOnLeaseExpiry (0.00s)
=== RUN   TestRenewExtendsLease
--- PASS: TestRenewExtendsLease (0.00s)
=== RUN   TestCenterRejectsExpiredLease
--- PASS: TestCenterRejectsExpiredLease (0.00s)
PASS
ok  	configlease
```

## 样例输出（`go run ./cmd/demo`）

```
== t=0ms: two nodes bind to config v1 ==
  [SELECT 1] OK -> node=fast version=1 dbpassword=s3cret-v1 query="SELECT 1"
  [SELECT 1] OK -> node=slow version=1 dbpassword=s3cret-v1 query="SELECT 1"

== t=600ms: center changes DB password, publishes v2, revokes old leases ==
  old lease of slow node still valid at center? false

== t=1000ms: fast node re-bound to v2; slow node still inside old lease TTL ==
  [SELECT 1] OK -> node=fast version=2 dbpassword=s3cret-v2 query="SELECT 1"
  [SELECT 1] OK -> node=slow version=1 dbpassword=s3cret-v1 query="SELECT 1"

== t=1700ms: slow node's old lease expired naturally (TTL 1500ms) ==
  [UPDATE accounts SET balance = balance - 1] OK -> node=fast version=2 dbpassword=s3cret-v2 ...
  [UPDATE accounts SET balance = balance - 1] REJECTED: configlease: no valid config lease, refusing traffic

== slow node reconnects, re-binds to v2, resumes serving ==
  [SELECT 1] OK -> node=slow version=2 dbpassword=s3cret-v2 query="SELECT 1"
```

## 文件结构

- `clock.go` — 时钟抽象（`RealClock` / 测试用 `ManualClock`）
- `center.go` — 配置中心：签发/续期/撤销租约，版本递增
- `node.go` — 工作节点：租约持有、续期、接客前 fencing 检查
- `configlease_test.go` — 验收测试（`ManualClock` 精确控制时间）
- `cmd/demo/main.go` — 可运行样例
