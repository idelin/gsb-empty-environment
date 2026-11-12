# postMessage发送目标强制

攻击者可在页面向iframe发消息前导航它，窃取随postMessage发出的令牌。为TypeScript前端实现可导入的消息发送组件：targetOrigin须为显式精确来源，禁用*；目标来源为opaque时不发敏感数据，改为先握手再传MessagePort，握手含随机挑战且只接受一次。用导航换站、无来源sandbox iframe和重复握手样例测试。

## 用法

### 精确来源（普通跨站 iframe）

```ts
import { SecureMessenger } from "./index.ts";

const messenger = new SecureMessenger({
  target: () => iframe.contentWindow, // 发送时才解析，避免持有旧引用
  targetOrigin: "https://app.example", // 必须显式精确来源；"*" 会在构造时抛 OriginPolicyError
});
await messenger.post({ token }); // 浏览器投递时按 iframe 当前来源校验，被导航后不送达
```

### opaque 来源（无 allow-same-origin 的 sandbox iframe）

目标来源序列化为 `"null"`，无法做精确匹配，因此敏感数据一律不进
`window.postMessage` 负载：先以随机挑战握手（每个挑战只接受一次），
再转移 `MessagePort`，之后的数据只经端口发送。

父页面：

```ts
const messenger = new SecureMessenger({
  target: () => iframe.contentWindow,
  targetOrigin: "null", // 声明目标为 opaque，启用握手 + MessagePort
});
await messenger.post({ token }); // token 只经 MessagePort 发送
```

嵌入页（iframe 内）：

```ts
import { attachHandshakeResponder } from "./index.ts";

attachHandshakeResponder({
  messageEvents: window,
  allowedOrigin: "https://parent.example", // 校验父页面精确来源
  onChannel: (port) => {
    port.addEventListener("message", (event) => {
      // 接收敏感数据
    });
    port.start?.();
  },
});
```

## 测试

```sh
npm test
```

覆盖：targetOrigin 校验（禁用 `*` 与非精确写法）、导航换站（发送前与
投递竞争中）、无来源 sandbox iframe 的握手 + MessagePort 流程、重复握手
与重放拒绝、并发 single-flight、握手超时不泄露、伪造父来源拒绝。
