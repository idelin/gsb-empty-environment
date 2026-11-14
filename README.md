# 本机消息主机被网页换了调用者

扩展的原生消息桥接按请求里的应用名转发，攻击者可让网页通过扩展调用任意本机主机并读取返回。请实现供TypeScript扩展后台调用的可导入本机桥接组件。调用方必须来自扩展自有页面或已声明content script的一次性会话，应用名与扩展ID、调用来源和权限清单三者绑定；网页消息不得直接触发本机连接。请求与响应按长度、字段与超时校验，子进程以最小权限启动，错误不泄露主机路径。用网页伪造应用名、跨扩展调用、会话重放、超长响应和合法扩展请求验证只有清单允许的主机能被调用。

## 组件用法

`index.ts` 导出 `NativeBridge`，由扩展后台在 `runtime.onMessage` 中持有并调用：

```ts
import { NativeBridge } from './index.ts';

const bridge = new NativeBridge({
  extensionId: 'abcdefghijklmnopabcdefghijklmnop',
  contentScripts: ['content/main'],          // 已声明的 content script
  hosts: {                                   // 权限清单：仅列出的主机可调用
    'com.example.host': {
      command: '/usr/local/libexec/my-host', // 路径只在清单中，错误消息永不回传
      args: [],
      allowedCallers: ['extension-page'],    // 应用名 × 调用来源绑定
      maxRequestBytes: 64 * 1024,
      maxResponseBytes: 1024 * 1024,
      timeoutMs: 10_000,
    },
  },
});

// content script 先换取一次性会话令牌（用后即焚、带过期时间）
const { token } = bridge.issueContentScriptSession('content/main');

// 后台消息入口：根据 sender 构造 CallerContext 后调用
const res = await bridge.handleMessage(message, {
  kind: 'extension-page',                    // 或 'content-script'；'web-page' 一律拒绝
  extensionId: sender.id,
  origin: sender.origin,
});
```

安全约束：

- 网页（`web-page`）消息在任何情况下都不会触发本机连接。
- 扩展页面调用要求 `extensionId` 与来源 origin 同时匹配清单绑定的扩展。
- content script 必须持有针对其 `scriptId` 签发的一次性会话令牌，重放、过期、跨脚本使用均被拒绝。
- 应用名必须命中清单，且该主机允许当前调用来源；请求/响应按长度、字段与超时校验。
- 子进程以空环境、无 shell 的最小权限启动；所有错误消息为静态文本，不泄露主机路径。

运行测试：`npm test`
