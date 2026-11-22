# Conditional Passkey Mediator

第三方广告框架可能通过条件式中介（conditional mediation / passkey autofill）探测访客是否持有通行密钥。
本 SDK 在 `navigator.credentials.get()` 发起前实施环境闸门，确保只有合法路径才能触达浏览器接口。

## 闸门规则（全部在平台调用之前执行）

1. **可信框架**：当前框架必须是顶层框架，或所有祖先框架均为同站（schemeful same-site，eTLD+1 一致）。
   跨站 iframe（例如第三方广告框架）直接以 `NotAllowedError` 拒绝，不会触达浏览器接口。
2. **权限策略**：`document.permissionsPolicy`（或旧版 `featurePolicy`）必须允许
   `publickey-credentials-get`；无策略对象时回退到 Permissions API，仍无法证明则嵌入式框架失败关闭。
3. **空 `allowCredentials`**：条件式中介要求 `allowCredentials` 为空数组，非空列表以
   `InvalidStateError` 拒绝；缺少 `challenge` 以 `TypeError` 拒绝。实际下发的请求总是被归一化为 `[]`。
4. **单请求不变量**：同一页面同一时刻至多存在一个条件式请求；新请求发起前先 `abort()` 旧请求，
   旧调用以 `AbortError` 拒绝。
5. **导航与晚到响应**：监听 `pagehide` / `beforeunload`，导航即中止在途请求并推进代次（generation）；
   导航后或被取代后才到达的平台响应一律丢弃，绝不会交还给旧调用方。

## 页面脚本用法

```ts
import { getConditionalPasskeyMediator } from './src/index.ts';

const mediator = getConditionalPasskeyMediator();

try {
  const credential = await mediator.get({
    publicKey: {
      challenge: new Uint8Array([/* CSP 随机值 */]),
      rpId: 'example.com',
      allowCredentials: [], // 条件式中介必须为空
    },
  });
  // 用户通过 autofill 选择了通行密钥
} catch (error) {
  // NotAllowedError：闸门拦截；AbortError：被新请求/导航/外部信号中止
}

// 可随时取消；再次 get() 会自动中止上一个请求
mediator.abort();
```

也可以通过 `new ConditionalPasskeyMediator(env)` 注入 `BrowserEnvironment`，便于在非浏览器环境测试。

## 测试

```sh
npm test
```

基于 Node 内置测试运行器（`node --test`，原生 TS 类型擦除），使用可注入的浏览器环境假件，覆盖：

- 跨站 iframe / 跨站祖先 / scheme 不一致：断言 `navigator.credentials.get` 调用次数为 0；
- 权限策略拒绝、Permissions API 拒绝、嵌入式框架无法证明授权：失败关闭；
- 非空 `allowCredentials`、缺少 `challenge`：平台接口零调用；
- 重复发起：旧请求信号被中止、全页仅一个在途请求；被闸门拒绝的重复请求不影响合法请求；
- 导航后晚到的成功响应与失败响应：均以 `AbortError` 结束，旧调用方永远收不到陈旧凭证；
- 外部 `AbortSignal`、`abort()`/`dispose()`、无 WebAuthn API 等边界情况。
