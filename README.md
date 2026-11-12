# 窗口消息发送者导航竞争（postMessage TOCTOU）

页面在验证 `postMessage` 来源之后、异步处理完成之前，发送者窗口可能已经
导航到另一个文档。浏览器里 `WindowProxy` 在导航后是**同一个 JS 对象**，
只是内部文档被替换；朴素代码缓存 `event.source` 后再调用
`source.postMessage(...)`，响应会发给新文档。`targetOrigin` 只能拦截
跨源导航，对**同源导航**完全无效。

本目录为 TypeScript 宿主实现一个消息接收组件，把"发送时的来源 + 文档代次"
固化为不可变可信元数据，回复能力只对发送时的那个文档有效，窗口对象复用
不会继承能力。

## 文件结构

- `src/types.ts`：抽象类型（可信请求、能力、导航事件、传输层、统计）。
- `src/receiver.ts`：`TrustedMessageReceiver` 核心安全组件。
- `src/sim.ts`：可控最小浏览器环境（可导航窗口、确定性任务队列、消息总线）。
- `test/receiver.test.ts`：12 个测试，含两个红队对照用例。
- `index.ts`：公共导出。

## 安全模型

1. **同步固化元数据**：消息送达回调里，在任何 `await` 之前同步抓取
   `origin` 与 `generation`。`origin` 由浏览器保证；`generation` 是宿主
   导航记账维护的单调递增文档代次，页面 JS 无法伪造。
2. **能力三元组**：回复能力由 `(窗口, origin, generation)` 唯一确定。
   应用拿到的 `TrustedRequest` 字段全部 `readonly`，内部另存一份能力记录，
   外部篡改请求对象不影响回复判定。
3. **活性闸门**：回复前强制校验窗口当前代次仍等于发送时代次、来源未变，
   并强制使用固化的来源作为 `targetOrigin`（绝不使用 `"*"`）。
4. **导航主动撤销**：导航事件到达时推进窗口代次，并撤销该窗口旧文档的
   全部未消费能力。
5. **一次性能力**：每个能力只能成功回复一次，重复回复返回
   `capability-consumed`，防止重放。

回复失败均通过 `ReplyOutcome` 显式返回（`window-navigated` /
`stale-generation` / `capability-revoked` / `capability-consumed` /
`transport-rejected`），且任何失败路径都不会向传输层外发数据。

## 真实宿主接入

- `MessageTransport` 适配 `window.addEventListener("message", ...)` 与
  `window.postMessage`；`RawMessageEvent.origin/generation` 中 origin
  直接取自浏览器事件，generation 取自宿主自己的导航计数器。
- `NavigationObserver` 在真实浏览器中可由宿主的 frame 生命周期记账实现
   （例如维护每个 frame 的导航序号，在 frame unload / 新文档 load 时递增），
  组件本身不依赖具体检测手段。
- 浏览器不向页面暴露原生"文档代次"，因此代次必须由可信宿主层维护，
  不能让被接收的页面自行声明。

## 运行测试

```sh
npm test
```

测试用 `TaskQueue` 精确编排"接收 → 挂起的异步校验 → 导航 → 回复"交错序列，
其中两个红队对照用例演示朴素实现如何在同源导航下把敏感响应泄漏给新文档，
而本组件在同样序列下拒绝回复。
