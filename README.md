# 租约控制器（Lease Controller）

支持**动态续期**与**平滑优雅移交（Graceful Handover）**的租约控制器，TypeScript 实现，
零第三方依赖，使用 Node.js 内置测试运行器（`node:test`）验证。

## 核心能力

- **租约获取与动态续期**：持有者按剩余 TTL 的比例动态计算续期时机（默认剩余一半时续期），
  失败时指数退避重试，耗尽重试后自动放弃主角色。
- **主动移交（Handover）协议**：持有者计划停机维护时，主动向协调中心申请移交并指定
  接盘候选人；协调中心签发携带**剩余租约上下文**（原到期时间、递增的 fencing epoch、
  一次性凭证）的交接包；候选人凭包**立即接管**，无需等待租约自然超时。
- **写冻结**：移交发起时旧主冻结本地写入，业务层短暂排队，切换完成后补发，全程零错误。
- **Fencing token**：epoch 单调递增，移交后旧 epoch 的写入被协调中心拒绝，防止脑裂双写。
- **可中止**：候选人拒收/超时时持有者可中止移交并恢复服务。

## 协议流程

```
节点A(主)                协调中心                     节点B(候选)
   |  滚动重启指令           |                           |
   |  freezeWrites()        |                           |
   |  initiateHandover(B)   |                           |
   |----------------------->|  租约冻结(handover-pending)|
   |<----- HandoverPackage--|  (剩余TTL/新epoch/一次性token)
   |            交接包(运维通道递交)                      |
   |------------------------------------------------>  |
   |                       |<----- completeHandover ----|
   |<-- handover-completed |  继承剩余上下文, epoch+1     |
   |  卸任, 进入维护         |---------- 接管主角色 ------>|
```

## 目录结构

- `src/types.ts` — 租约上下文、交接包、事件与错误类型
- `src/coordinator.ts` — 协调中心：签发/续期/移交/fencing 校验
- `src/node.ts` — 节点侧代理：动态续期循环、写冻结、移交与接管
- `test/lease-controller.test.ts` — 10 个 `node:test` 用例
- `examples/rolling-restart-demo.ts` — 滚动重启移交运行样例

## 运行

要求 Node.js ≥ 22.6（使用 `--experimental-strip-types` 原生运行 TypeScript）。

```bash
npm test        # 运行全部测试
npm run demo    # 运行滚动重启移交样例
```

## 实测结果

测试（Node v22.14.0）：

```
# tests 10
# pass 10
# fail 0
```

运行样例输出：

```
=== 租约控制器：滚动重启下的平滑优雅移交 ===
[t0] 节点 A 获取租约成为主节点 (leaseId=6a0a587c…, epoch=1, TTL=5000ms)
[t0] 业务写入正常: 2 条已提交到 A
[指令] 运维下发滚动重启指令 → 节点 A 计划停机维护
[A] 已冻结本地写入，向协调中心申请移交，指定候选人 B
[A] 交接包签发: 剩余TTL=4999ms, 新epoch=2, 到期时间原样保留
[业务] 移交窗口内 1 条写入进入暂存队列（队列长度=1）
[B] 接收交接包并立即接管主角色 (epoch=2, generation=1)
[B] 继承剩余租约上下文: 到期时间不变，剩余 4999ms
=== 验收结果 ===
  业务切换耗时        : 0.311 ms
  写入错误数          : 0
  已提交写入          : order#1→A, order#2→A, order#3→B, order#4→B
  A 状态              : isPrimary=false（已卸任，可安全重启）
  B 状态              : isPrimary=true, 动态续期循环运行中
✔ 零错误中断，毫秒级平滑移交完成，A 可安全进入滚动重启
```
