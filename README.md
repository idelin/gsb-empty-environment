# Hardened Electron `contextBridge` Wrapper

攻击者控制的渲染页可借预加载桥传入 `Proxy`、getter 或 `__proto__` 对象，诱导特权
IPC 执行非预期逻辑。本包为 TypeScript Electron 应用提供一个可导入的 `contextBridge`
封装，在渲染进程与主进程两侧同时收敛攻击面。

## 安全目标

- 只暴露**显式登记**的方法名；未登记通道在主进程根本没有 `ipcMain.handle` 监听器。
- 参数在主进程先 `structuredClone` 成快照，再按**每个通道的 schema** 校验。
- 克隆前后递归扫描，拒绝函数、symbol、getter/setter、`Proxy` 异常 trap、循环引用。
- 拒绝污染键 `__proto__`、`constructor`、`prototype` 以及非普通对象（如类实例、`Date`）。
- 每个处理器先核对 `event.senderFrame` 的来源 URL/origin，默认拒绝子框架与 opaque origin。
- 暴露面为 `Object.create(null)` + `Object.freeze`，渲染页无法增删或改写桥方法。

## 模块结构

| 文件 | 职责 |
| --- | --- |
| `src/registry.ts` | 通道登记表，校验通道名/暴露名，禁止保留字与重复登记 |
| `src/sanitize.ts` | `scanValue` 预扫描与 `safeClone` 快照，拦截 getter/Proxy/污染键 |
| `src/schema.ts` | 零依赖 schema 校验器（原始值、数组、对象、union、enum） |
| `src/main.ts` | `senderFrame` 校验 + 主进程克隆/校验管线 + `ipcMain.handle` 注册 |
| `src/preload.ts` | 生成只含登记方法的冻结桥对象（不依赖 Electron，可单测） |
| `src/preload-electron.ts` | 预加载入口，调用 `contextBridge.exposeInMainWorld` |
| `src/errors.ts` | 带错误码的类型化错误 |

## 接入方式

登记表由主进程和预加载脚本共享（见 `example/registry.ts`）：

```ts
export const registry = new ChannelRegistry([
  {
    channel: 'app:save-profile',
    exposedName: 'saveProfile',
    schema: {
      type: 'object',
      properties: { name: { type: 'string' } },
      additionalProperties: false,
    },
  },
]);
```

主进程（`example/main.ts`）：

```ts
registerBridgeHandlers(
  ipcMain,
  registry,
  { allowedOrigins: ['https://app.example.com'], requireMainFrame: true },
  (event, channel, safeArg) => handlePrivilegedCall(channel, safeArg),
);
```

预加载（`example/preload.ts`）：

```ts
exposeBridge('secureBridge', registry);
// 渲染页只拿到 window.secureBridge.saveProfile(arg)
```

## 防御流水线

1. 渲染进程调用桥方法，预加载侧先 `scanValue`，getter 不被执行即拒绝。
2. IPC 传输（Electron 自身做一次结构化克隆，函数本就无法跨越）。
3. 主进程 `safeClone` 再次克隆并二次扫描，得到与外部对象脱钩的纯数据快照。
4. 按登记 schema 校验，未知字段、类型不符、枚举越界一律拒绝。
5. `verifySenderFrame` 核对 `event.senderFrame.url/origin`、主框架状态与可选自定义规则。
6. 全部通过后，特权处理器才接触数据；未登记通道没有任何监听器。

## 测试

`test/bridge.test.ts` 使用假的 `ipcMain`/`ipcRenderer` 对，覆盖：

- 函数、getter/setter、`Proxy` trap、`__proto__`/`constructor`/`prototype` 污染键。
- 恶意对象端到端被拒，且 getter/trap 不被执行、全局原型未被污染。
- 未登记通道（如 `system:shell`）无监听器，调用抛 `UnregisteredChannelError`。
- `senderFrame` 来源 origin、子框架、缺失 frame、自定义校验的拒绝路径。

```sh
npm install
npm test          # node --experimental-strip-types --test test/**/*.test.ts
npm run typecheck # tsc --noEmit
```

> 当前沙箱内的 Node 动态库不可用，无法在此环境执行测试；代码与测试均按
> Node 内置 test runner（`--experimental-strip-types`）约定编写，安装依赖后即可运行。
