# Electron桥接参数消毒

攻击者控制的渲染页可借预加载桥传入Proxy、getter或__proto__对象调用特权IPC。为TypeScript Electron应用实现可导入的contextBridge封装：只暴露登记名称的方法，参数在主进程克隆后按schema校验，拒绝函数、getter和污染键；处理器核对event.senderFrame的URL。用恶意对象和未登记通道测试。

## 安全模型

- 单一固定通道 `secure-bridge:invoke`，渲染端只能调用登记在 `BridgeDefinition` 中的方法名；未登记名称（含 `constructor`、`__proto__`）在主进程直接拒绝。
- 入参先经 `safeClone` 消毒再 `structuredClone`：
  - 克隆前递归扫描，拒绝函数、symbol 键、getter/setter 访问器（不触发 getter）、`__proto__`/`constructor`/`prototype` 污染键，Proxy 的恶意 trap 在扫描阶段即暴露。
  - 克隆产生与渲染端对象彻底脱钩的数据副本，处理器无法被后续突变影响。
- 克隆后按声明式 schema（`s.string()`、`s.object(...)` 等）校验，默认拒绝未知字段、NaN 与类型不匹配。
- 主进程依据 `event.senderFrame.url` 做来源核对：支持默认来源策略与方法级覆盖（字符串 origin、`Set` 或自定义谓词）。
- 预加载端只暴露登记方法的薄封装，不暴露 `ipcRenderer`、`invoke` 等通用能力，API 对象冻结且以 null 原型创建。

## 用法

主进程：

```ts
import { registerBridge, s } from './index.ts';
import { ipcMain } from 'electron';

const unregister = registerBridge(
  ipcMain,
  {
    saveProfile: {
      params: [
        s.object({
          name: s.field(s.string()),
          age: s.optional(s.number()),
        }),
      ],
      handler: (_event, profile) => persist(profile),
    },
    adminAction: {
      params: [],
      allow: 'https://admin.example.com',
      handler: () => runAdminTask(),
    },
  },
  'https://app.example.com',
);
```

预加载脚本：

```ts
import { exposeBridge, s } from './index.ts';
import { contextBridge, ipcRenderer } from 'electron';

exposeBridge(
  contextBridge,
  ipcRenderer,
  {
    saveProfile: {
      params: [s.object({ name: s.field(s.string()) })],
      handler: () => undefined,
    },
  },
);

// 渲染端：window.secureBridge.saveProfile({ name: 'alice' })
```

## 测试

```sh
npm test
```

`test/secure-bridge.test.ts` 使用 `node:test` 与内存 mock（无需启动 Electron），覆盖：

- getter/setter 访问器拒绝且 getter 永不执行；
- `__proto__`、`constructor` 污染键（含嵌套与 Proxy 隐藏）拒绝；
- 走私函数的 Proxy、非克隆参数拒绝；
- 未登记通道、畸形信封、schema 未知字段拒绝；
- `senderFrame` URL 默认策略与方法级策略；
- 处理器收到的是脱钩克隆；预加载 API 冻结且仅含登记方法。
