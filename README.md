# module-lifecycle

模块生命周期库（TypeScript）：允许反复装载同名模块，每次装载返回对应的释放函数；
旧释放函数晚调用不会卸载新世代模块，重复释放无副作用。依赖通过引用登记管理，
卸载时逆序清理；装载失败自动回滚，依赖循环可被检测；全程输出可解释的生命周期日志。

## 快速开始

```bash
npm install   # 安装 typescript / @types/node
npm test      # 编译并运行 node:test 测试
```

## 用法

```ts
import { ModuleLifecycle } from "./src/lifecycle";

const kernel = new ModuleLifecycle();

kernel
  .define({ name: "config", setup: () => loadConfig(), teardown: (c) => c.close() })
  .define({ name: "db", dependencies: ["config"], setup: () => connect(), teardown: (d) => d.close() })
  .define({ name: "service", dependencies: ["db"], setup: () => startService() });

const h1 = kernel.load("service"); // 递归装载 config -> db -> service
const h2 = kernel.load("service"); // 复用存活实例，引用计数 +1

h1.release(); // 计数归零前不会卸载
h2.release(); // 计数归零：service 卸载，随后逆序释放 db、config

console.log(kernel.formatLog());
```

## 语义说明

- **世代（generation）**：同名模块每次从“未装载”状态重新装载时世代号递增。
  句柄绑定到具体世代记录，旧世代的释放函数在新世代上调用会被忽略（记 `release:ignored` 日志）。
- **幂等释放**：同一一句柄的 `release()` 只有第一次生效，之后为无副作用操作。
- **引用登记**：装载时按 `dependencies` 声明顺序递归装载依赖，并在当前记录中登记依赖租约。
- **逆序清理**：引用归零时先执行本模块 `teardown`，再按登记逆序释放依赖；
  `shutdown()` 按装载逆序卸载全部模块。`teardown` 抛错不会中断清理，仅记 `unload:failure`。
- **失败回滚**：装载中途（依赖装载或 `setup`）失败时，本层已登记的依赖按逆序回滚，
  错误以 `ModuleLoadError`（含 `cause`）抛出；依赖循环抛 `DependencyCycleError`，
  `path` 字段给出完整循环路径。
- **日志**：所有事件进入 `kernel.events`（含递增 `seq`、类型、模块、中文描述、结构化
  `details`），`formatLog()` 渲染为可读文本。

## 错误类型

`ModuleNotDefinedError` / `ModuleAlreadyDefinedError` / `DependencyCycleError` / `ModuleLoadError`

## 备注

装载与卸载过程为同步设计；`setup`/`teardown` 若返回 Promise 不会被等待。
