# 浏览器用户激活消费（激活令牌状态机）

安全操作（`window.open`、剪贴板、支付等）需要一次真实用户激活，异步任务链不能
无限延长它。本包是供 TypeScript 宿主嵌入的用户激活状态机，零运行时依赖，用 Node
内置 test runner + 假时钟做确定性测试。

## 模型

- 令牌 `ActivationToken` 绑定**一个文档**与**一个可信事件**（`isTrusted`），带
  `issuedAt/expiresAt` 期限；伪造事件不会产生令牌。
- 状态机：`active -> consumed | expired | revoked | superseded`，非激活态均为终态。
  - `consumed`：消费类操作同步 check-and-flip 原子领取，一次手势只能成功一次。
  - `expired`：惰性判定，`now >= expiresAt` 的同一刻即过期（期限前一刻仍有效）。
  - `revoked`：文档导航/销毁立即撤销，并清空粘性激活。
  - `superseded`：新可信事件铸造新令牌、开启新期限，旧在途令牌被取代。
- 非消费类操作（`inspect` / `hasTransientActivation`）只检查，不改变可用性。
- 跨文档消费被拒绝（`cross-document`），且失败不灼伤原文档令牌；严格 API 抛
  `CrossDocumentTransferError`。
- 微任务在入队任务的同一时间戳执行，时钟不前进，因此微任务不能续期；只有新的
  可信事件才能给文档新的期限。

## 文件

- `src/activation.ts` — 令牌与 `UserActivationService` 状态机、审计账本。
- `src/clock.ts` — `VirtualClock`：假时钟 + 宏任务/微任务队列，时间只随测试推进。
- `src/host.ts` — `SimulatedBrowserHost`：窗口/文档/事件宿主，`openWindowFrom`
  是典型消费类操作（一次激活最多放行一个弹窗）。
- `src/errors.ts` — 类型化错误。
- `test/activation.test.ts` — 18 个测试，覆盖下列场景。

## 测试覆盖

- 同时打开两个窗口：首个 `window.open` 原子领取，第二个以 `consumed` 拦截；
  微任务中的尝试同样被拦截且时钟不变。
- 过期同刻：TTL-1 仍可用、TTL 当刻 `expired`；跨期限微任务不可用且不续期；
  异步任务链无法延长激活。
- 跨文档转移拒绝：他文档令牌不能当凭证，原令牌保持 `active`，本文档仍可领取。
- 导航撤销、可信事件门槛、非消费检查不消耗、新事件取代旧令牌、审计账本。

## 运行

```sh
node --test "test/*.test.ts"     # Node >= 23.6，原生类型擦除
npm test
```
