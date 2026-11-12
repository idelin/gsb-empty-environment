# Capacitor插件桥来源与原型键

混合应用的原生桥收到网页消息 {callbackId, pluginId, methodName, options}，攻击者页面或嵌入的 iframe 调用 Filesystem 等特权插件，或把 pluginId 设成 __proto__ 与 constructor。为TypeScript宿主实现可嵌入的桥分发组件：来源主机必须在 allowNavigation 内，*.example.com 只匹配子域而不匹配 example.com、evilexample.com 与 example.com.evil.net，iframe 一律无权调用；插件与方法走显式白名单，按自有属性查找而不是对象属性链；callbackId 须为短字母数字，同一 callbackId 只能完成一次，options 的键含 __proto__ 即拒。用原型键、仿冒域、iframe 和重复回调验证。

## 结构

- `src/origin-policy.ts` — `allowNavigation` 解析与来源主机匹配
- `src/options-guard.ts` — `options` 原型键递归检查（含循环引用/深度保护）
- `src/bridge.ts` — `BridgeDispatcher` 分发器与 `attachBridge` 窗口适配层
- `index.ts` — 公共导出
- `test/` — `node:test` 套件，`npm test` 运行

## 安全模型

- **来源校验**：`*.example.com` 要求至少一个子域标签，拒绝 apex `example.com`、后缀仿冒 `evilexample.com` 与 `example.com.evil.net`；主机经 `URL` 规范化（小写、去尾点），端口与路径不参与主机匹配。
- **iframe 拒绝**：仅当 `event.source === window`（主窗口自身）时放行；iframe、弹窗、opener 等跨窗口来源一律 `IFRAME_FORBIDDEN`。
- **显式白名单**：插件/方法表在构造时复制到 `Object.create(null)` 注册表，查找只用 `hasOwn`，`__proto__`/`constructor`/`prototype` 键在入站与注册阶段都被拒，绝不走属性链。
- **回调一次性**：`callbackId` 限 1–32 位字母数字；id 在调用处理器前预占，重复 id 静默丢弃（不执行插件、不回发第二条响应）。
- **options 防护**：任意层级出现含 `__proto__` 子串的键即 `OPTIONS_REJECTED`，仅接受普通对象/数组，`Object.prototype` 不被污染。

## 用法

```ts
import { attachBridge, BridgeDispatcher } from './index.ts';

const dispatcher = new BridgeDispatcher({
  allowNavigation: ['*.example.com', 'trusted.example.org'],
  plugins: {
    Filesystem: {
      methods: {
        readFile: async (options) => ({ path: options?.path, data: '...' }),
      },
    },
  },
});

const detach = attachBridge(dispatcher, window);
// detach() 移除 message 监听
```

非 DOM 宿主可直接调用 `dispatcher.dispatch(event, event.source === hostWindow)`，
返回判别联合 `DispatchResult`，由宿主决定如何回发。
