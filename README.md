# 授权令牌越过KMS grant约束

密钥服务的grant允许特定操作和上下文，若传播令牌只按grantID查找，攻击者可换主体、操作或资源。给Go KMS宿主实现可导入的grant状态包：grant含grantID、keyID/版本、granteePrincipal、retiringPrincipal、允许操作集合、上下文等值约束、可选调用上下文必须包含的subset约束、创建代次和状态Pending/Active/Retired；规范上下文按长度编码。Create先持久Pending，再由权限回调确认创建者对key和全部约束有Grant权限，Active后签发grantToken=HMAC-SHA256服务密钥覆盖grant完整摘要、随机tokenID和到期时刻。鉴权请求同时带调用主体、操作、keyID/版本、上下文和token；先验token，再读取同代grant，主体/操作/key/上下文均满足且未Retired才放行。Pending token只在调用方显式允许的由宿主单调时钟判断的传播窗口内使用，且必须对应Create请求的requestID与相同主体，不能被其他客户端拿来绕过尚未一致的索引。Retire仅retiringPrincipal或grantee可执行，原子递增grant代次，使全部旧token失效；key轮换不自动扩大到新版本。实现待审cacheKey差分搜索和并发Create/Use/Retire调度，找弱实现成功调用Decrypt却不满足约束的最短轨迹。覆盖subset键缺失、多余上下文、token跨主体、Pending转Active和响应丢失。

## 实现

- `grant/`：可导入的生产状态机。`Store` 用单互斥锁线性化 Create/Authorize/Retire；
  上下文按 4 字节长度前缀规范编码；令牌为 `HMAC-SHA256` 覆盖完整 grant 摘要、
  随机 tokenID 与到期时刻；`BeginCreate`/`Activate` 暴露 Pending→Active 两阶段，
  `Create` 是其便捷组合，`(Creator, RequestID)` 幂等键处理响应丢失。
- `grantsweep/`：差分搜索。参考模型（`Policy{}` 全关）与单弱点模型对照，BFS
  枚举 Create/Use/Retire/轮换/时钟动作，输出弱实现放行而参考模型拒绝的最短轨迹；
  `SearchCacheKeyCollisions` 对比强 cacheKey 与仅 GrantID 弱键；`SearchRetireRace`
  把 Retire 与 Use 拆微步穷举交织，复现非原子 Retire 的 TOCTOU。
- `cmd/grantsweep/`：打印 cacheKey 差分、BFS 最短轨迹与并发调度结果。

## 覆盖的弱实现场景

| 场景 | 弱点策略 |
| --- | --- |
| subset 键缺失 | `IgnoreSubset` |
| 多余上下文 | `IgnoreExtraContext` |
| token 跨主体 | `IgnoreCaller` |
| 操作越权 | `IgnoreOperation` |
| key 轮换扩权 | `IgnoreKeyVersion` |
| 只按 grantID 查找 | `GrantIDLookup` |
| Pending 跨客户端 | `CrossClientPending` |
| 非原子 Retire 竞态 | `NonAtomicRetire`（调度器） |

## 运行

```sh
go test -race ./...
go run ./cmd/grantsweep
```
