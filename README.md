# 围栏框架不透出来源

围栏（fenced frame）内的文档不得读取嵌入方地址，也不得向顶层窗口发消息；网络目标
只能是策略显式列出的源，围栏自身的同源资源请求保持可用。本仓库提供一个可导入的
TypeScript 裁决库，由嵌入壳在围栏窗口上安装并执行裁决。

## 威胁模型

围栏内的文档是不可信内容，它可能尝试：

- 经 `window.top` / `window.parent` / `document.referrer` /
  `document.ancestorOrigins` / `window.frameElement` 读取外层文档地址；
- 经 `postMessage` 向 `top`、`parent` 或嵌入框架元素发送消息；
- 经 `fetch`、`XMLHttpRequest`、`navigator.sendBeacon`、`WebSocket`、
  `EventSource` 向策略之外的网络目标外传数据。

## 裁决规则

| 操作 | 裁决 |
| --- | --- |
| 读取外层地址（任何属性路径） | 始终拒绝，理由 `outer-url-disclosed` |
| 向 `top` / `parent` / `frameElement` 发消息 | 始终拒绝，理由 `top-communication-disabled` |
| 围栏内 `self` 消息 | 允许，理由 `self-message-allowed` |
| 同源网络请求（含相对 URL、同源 `blob:`） | 允许，理由 `same-origin-allowed` |
| 策略 `allowedOrigins` 列出的源 | 允许，理由 `allowlisted-origin`（方案/主机/端口精确匹配） |
| 未列出的跨源目标 | 拒绝，理由 `origin-not-allowlisted` |
| `data:` / `javascript:` 等不透明源 | 拒绝，理由 `opaque-origin` |
| 无法解析的 URL | 拒绝，理由 `unparseable-url` |

源比较基于序列化后的 `URL.origin`，因此协议、主机、端口全部参与匹配（例如
`wss://api.example` 与 `https://api.example` 是两个不同的源）。URL 中的 userinfo
不参与裁决，`https://fence.example@evil.example/` 的目标源是 `evil.example`。

## 模块结构

- `src/origin.ts`：源规范化、相对 URL 解析、同源比较。
- `src/policy.ts`：`FencePolicy` 声明与 `compilePolicy` 校验（拒绝不透明源、
  基址跨源等非法配置）。
- `src/adjudicator.ts`：纯函数式裁决器 `FenceAdjudicator`，无 DOM 依赖，
  返回结构化 `FenceDecision`，可单独使用或嵌入自定义外壳。
- `src/enforcer.ts`：`installFence` 在围栏窗口上安装属性陷阱与网络通道包装，
  拒绝时抛出 `FenceViolationError`，并支持 `onViolation` 审计回调与 `uninstall` 还原。
- `index.ts`：统一导出入口。

## 嵌入壳接入

纯裁决（自行决定如何执行）：

```ts
import { createAdjudicator } from './index.ts';

const judge = createAdjudicator({
  fenceOrigin: 'https://fence.example',
  allowedOrigins: ['https://api.example'],
});

// 网络出口前检查
const decision = judge.adjudicateNetworkRequest('https://tracker.example/x');
if (!decision.allowed) {
  // 阻断请求并记录 decision.reason
}
```

在围栏窗口上安装完整强制层：

```ts
import { installFence } from './index.ts';

const enforcement = installFence(fenceWindow, {
  fenceOrigin: 'https://fence.example',
  documentBaseUrl: 'https://fence.example/app/index.html',
  allowedOrigins: ['https://api.example', 'wss://api.example'],
}, {
  onViolation: (decision, context) => auditLog(decision, context),
});

// window.top / parent 不再暴露外层地址；顶层 postMessage 与越源网络请求抛
// FenceViolationError；同源请求正常工作。需要拆除时调用：
enforcement.uninstall();
```

## 测试

```sh
npm test
```

测试基于 Node 内置 test runner（`--experimental-strip-types` 直接执行 TS），
覆盖全部裁决分支以及 mock DOM 下的属性隔离、各网络通道拦截和卸载还原。
