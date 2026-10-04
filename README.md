# module-lifecycle

一个 TypeScript 模块生命周期库：同名模块可反复装载，每次装载返回一个对应的释放函数；依赖在装载时登记、卸载时按逆序清理，装载失败与依赖循环都会整体回滚，并输出可解释的中文生命周期日志。

## 核心语义

- **反复装载同名模块**：实例存活期间多次 `load` 共享同一实例，引用计数归零才真正卸载；每次卸载后再装载会产生新的"代次"（generation）。
- **每次装载返回对应的释放函数**：`load` 返回一次性、幂等的 `release` 函数，它绑定到具体的实例代次——旧代次的释放函数晚调用不会卸载新实例，重复调用无副作用（日志会说明忽略原因）。
- **引用登记与逆序清理**：工厂函数内通过 `ctx.require(dep)` 装载依赖并登记到当前模块；卸载时先逆序执行清理回调（`ctx.onDispose` / `define` 的 `dispose`），再逆序释放依赖。
- **失败回滚**：工厂抛错时，已登记的依赖按逆序回滚，抛出带 `cause` 的 `ModuleLoadError`。
- **依赖循环**：装载链上检测环，抛出带 `cyclePath` 的 `DependencyCycleError`，已装载一半的现场整体回滚，装载器恢复可用。
- **可解释日志**：每个生命周期事件都带中文说明（为什么复用、为什么忽略释放、按什么顺序清理），默认输出到控制台，可替换为 `MemoryLogger`（收集并渲染）或 `SilentLogger`（静默）。

## 快速开始

```ts
import { ModuleLoader } from "module-lifecycle";

const loader = new ModuleLoader();

loader.define("config", () => ({ dbUrl: "sqlite://demo.db" }));
loader.define("db", (ctx) => {
  const config = ctx.require<{ dbUrl: string }>("config");
  ctx.onDispose(() => closeConnection());
  return connect(config.dbUrl);
});

const release = loader.load("db"); // 返回释放函数，release.instance 是模块实例
release();                          // 引用归零：先清理 db，再逆序释放 config
release();                          // 无副作用，日志说明"重复释放"
```

## API

- `new ModuleLoader({ logger? })`
- `loader.define(name, factory, { dispose? })`：注册模块定义；同名重复定义抛错。
- `loader.load<T>(name): Release<T>`：装载模块。`Release` 是可调用对象，附带 `instance` / `moduleName` / `generation` / `released`。
- `loader.isLoaded(name)` / `loader.refCount(name)`：查询存活状态与引用计数。
- `ctx.require<T>(name)`：在工厂内装载并登记依赖（请用它而不是直接 `loader.load`，否则依赖不会被跟踪清理）。
- `ctx.onDispose(fn)`：登记清理回调。
- 错误类型：`ModuleNotDefinedError`、`DependencyCycleError`（含 `cyclePath`）、`ModuleLoadError`（含 `cause`）。
- 日志器：`ConsoleLogger`（默认）、`MemoryLogger`（`events` / `render()`）、`SilentLogger`。

## 运行

需要 Node.js >= 22.18（原生运行 TypeScript）：

```sh
npm test    # node --test test/
npm run demo
```

## 设计说明

- **代次（generation）**：同名模块每次重新装载递增代次，释放函数闭包绑定代次对应的记录，从机制上保证"旧释放函数碰不到新实例"。
- **释放函数一次性**：每个 `release` 内部有消费标记，重复调用只记录日志。
- **清理顺序**：卸载 = 逆序清理回调 → 逆序释放依赖；回滚走同一条路径，保证失败与正常卸载行为一致。
- **清理回调容错**：单个 `dispose` 抛错只记录 `dispose-error` 日志，不中断其余清理，`release` 永不抛错。
