# 剪贴板异步许可绑定

浏览器宿主弹出剪贴板许可后，页面可能导航、失焦或切换到另一个标签页。本包把每次读写绑定到四元组：

- 标签页与文档身份（`tabId`、`documentId`）
- 单调递增的文档代（`documentGeneration`）
- 单调递增的焦点代（`focusGeneration`）
- 一次具体的用户激活（`activationId`、`activationExpiresAt`）

许可回调到达时，组件重新读取这四项并逐项比较。只有全部一致、激活未过期、动作未取消，才会同步调用系统剪贴板。每个请求还带有独立的 `requestId` 和一次性 `token`，因此一个请求失败或迟到的回调不会清除、借用或消费另一个请求的许可。

## 用法

```ts
import {
  createClipboardActions,
  type ClipboardEnvironment,
  type ClipboardPermissionGranter,
} from "./index.ts";

const environment: ClipboardEnvironment = {
  getDocument: () => host.activeDocument(), // tabId/documentId/generation
  getFocus: () => host.focusSnapshot(),      // generation/tabId|null
  getUserActivation: () => host.userActivation(), // activationId/expiresAt|null
  now: () => Date.now(),
};

const permissions: ClipboardPermissionGranter = {
  request: async (request) => host.showClipboardPrompt(request),
  cancel: (requestId, token) => host.dismissClipboardPrompt(requestId, token),
};

const actions = createClipboardActions({
  environment,
  permissions,
  systemClipboard: navigator.clipboard,
});

const result = await actions.copy("text");
if (!result.ok) {
  console.warn("clipboard denied:", result.reason);
}
```

`copy`/`writeText` 与 `read`/`paste`/`readText` 都返回判别式结果：

```ts
type ClipboardActionResult<T> =
  | { ok: true; value: T; requestId: string }
  | { ok: false; reason: ClipboardFailureReason; requestId?: string; error?: unknown };
```

失败原因包括 `navigation`、`focus-change`、`activation-expired`、`activation-changed`、`cancelled`、`permission-mismatch` 等。任何失败都不会调用 `systemClipboard`。

## 宿主契约

- `getDocument()`、`getFocus()`、`getUserActivation()` 必须返回调用瞬间的快照，不能返回可被后续修改的共享对象。
- `documentGeneration` 在导航时递增；`focusGeneration` 在每次焦点移动时递增，包括离开后回到同一标签页。
- `getUserActivation()` 应返回最近一次激活（即使已过期），这样组件能区分“从未激活”和“激活过期”；`null` 表示没有激活。
- 许可器必须原样回传 `requestId` 和授权时的 `token`。`cancel(requestId, token)` 只能影响对应请求。

## 可暂停许可器

`PausableClipboardPermissionGranter` 让测试精确控制回调时序：

```ts
const granter = new PausableClipboardPermissionGranter();
const pending = actions.copy("text");
const request = granter.pendingRequests[0];

// 在回调前模拟导航、切换焦点、激活过期或取消动作
granter.grant(request.requestId);
const result = await pending;
```

`grant`、`deny`、`cancel`、`settle` 都只结算指定 `requestId`；重复结算返回 `false`。`settle` 可用于模拟宿主回传错误 token 或错误 request id 的越权回调。

## 测试

```sh
npm test
```

测试覆盖多标签页交错回调、导航、焦点离开后返回、激活过期/替换、复制动作取消、迟到回调、伪造 token、重复回调、读操作以及系统剪贴板异常；所有失败路径都断言剪贴板调用次数为零。
