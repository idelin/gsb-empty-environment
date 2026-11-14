# 安全的扩展内容脚本桥

这是一个不依赖框架的 TypeScript 内容脚本核心，用来替代“从页面 DOM
读取扩展配置”的做法。安全策略只来自扩展清单和经过扩展运行时校验的消息；
页面提供的 DOM、窗口消息和端口全部按不可信输入处理。

## 安全不变量

- `loadManifestConfig()` 只读取扩展清单的 `host_permissions` 和可选的
  `extension_security` 段，从不查询页面 DOM。
- 受信配置消息必须来自 `runtime.id` 对应的扩展页面；页面 URL、继承属性、
  未知字段和 `__proto__`/`constructor`/`prototype` 均被拒绝。
- 受信消息只能收窄清单策略：目标来源、操作、消息大小和字符串长度不能扩大。
- 页面请求必须包含固定的字段集合，每个字符串、数组、枚举和字节数都有上限。
- 页面不能提交端口。内容脚本只为通过 `event.source`、`event.origin` 和
  `event.ports.length === 0` 检查的握手创建新的 `MessageChannel`。
- 每个端口只接受一个请求。响应后端口关闭；新的握手会吊销旧端口。
- `postMessage` 的 `targetOrigin` 必须是当前帧的精确来源，禁止 `"*"`。
- 特权 dispatcher 是模块闭包，不挂到 `window`，页面只能得到经过校验的
  请求对应的序列化结果。
- 页面锚点按固定直接子元素路径选择；重复或缺失候选一律拒绝。锚点只可用于
  非特权 UI，不会参与策略或目的地选择。

## 清单配置

```json
{
  "manifest_version": 3,
  "name": "example-extension",
  "version": "1.0.0",
  "host_permissions": ["https://api.example.com/*"],
  "content_scripts": [
    {
      "matches": ["https://app.example/*"],
      "js": ["content.js"]
    }
  ],
  "extension_security": {
    "version": 1,
    "allowedOrigins": ["https://api.example.com"],
    "allowedOperations": ["status", "relay"],
    "maxPageMessageBytes": 16384,
    "maxPageStringLength": 4096,
    "portIdleTimeoutMs": 60000,
    "handshakeCooldownMs": 250,
    "pageBridgeEnabled": true
  }
}
```

`extension_security` 可为构建期附加策略；缺失时使用
`host_permissions` 中的精确来源和代码内置的安全默认值。
`host_permissions` 只接受精确主机，`*://*.example.com/*` 这类通配主机
会在加载时被拒绝，避免隐式扩大目标集合。清单不合法时构造函数抛错，不安装
任何监听器，属于 fail-closed。

## 内容脚本接入

```ts
import { ExtensionContentScript } from "./index.ts";

const script = new ExtensionContentScript({
  runtime: chrome.runtime,
  hostWindow: window,
  documentLike: document,
  dispatch: async (request) => {
    // 推荐由后台页/服务工作线程执行特权操作，并在后台再校验一次。
    return { ok: true, value: "accepted" };
  },
});

script.start();
```

`dispatch` 收到的对象已经冻结，`targetOrigin` 已确认在清单允许集合内。
实际跨域请求应通过扩展后台执行，后台仍需重新校验来源、方法和重定向目标；
内容脚本不是网络权限的最终裁决点。

## 页面协议

页面先向当前帧发送无端口的 hello：

```ts
window.postMessage(
  {
    type: "extension:bridge-hello",
    protocolVersion: 1,
    requestId: "page-123"
  },
  location.origin,
);
```

内容脚本核对 `event.source === window`、`event.origin ===` 初始化时捕获的
精确来源、且 `event.ports.length === 0` 后，创建一次性 `MessageChannel`，
以精确 `targetOrigin` 发送 ready 消息并转移一个端口。页面使用该端口发送
唯一一个请求：

```ts
{
  type: "extension:bridge-request",
  protocolVersion: 1,
  sessionId,
  requestId: "request-123",
  operation: "relay",
  targetOrigin: "https://api.example.com",
  body: "bounded payload"
}
```

成功响应包含 `type: "extension:bridge-response"`、对应的 `sessionId` 和
`requestId`、`ok` 及有长度限制的 `value`。非法请求不会返回错误细节，只会
关闭端口。

## 结构选择

`findPageAnchor()` 固定遍历
`document.children -> html -> body -> main` 的直接子元素。它不读取
`id`、`name`、类名、属性或页面提供的 CSS 选择器。任一步出现零个候选返回
`missing`，出现多个候选返回 `ambiguous`，调用方不得在失败时回退到页面中的
同名元素。

## 验证

运行：

```sh
npm test
```

测试覆盖以下攻击面：

- DOM 覆盖：伪造 `meta`/`main`、同名属性和重复 `main`，策略与目的地不变。
- 原型污染：`__proto__`、继承字段、稀疏数组和污染后的原型不进入策略。
- 伪造消息端口：带端口的 hello、错误来源、错误 `origin` 和跨帧 `source`
  全部被忽略。
- 跨帧注入：同源 iframe 的 `source` 不匹配当前 host window，无法打开端口。
- 合法交互：白名单目标返回结果；白名单外目标、超长请求和额外字段被拒绝。
