# Persistent Reactive Store

持久化响应式存储与增量 Hydration 恢复器。纯 TypeScript，零依赖，直接使用
Node.js 原生 TypeScript（type-stripping）与内置测试运行器 `node:test`。

## 要求

- Node.js ≥ 23.6（原生运行 `.ts`，无需编译）
- 或 Node.js ≥ 22.6，运行时加 `--experimental-strip-types`

## 快速开始

```bash
npm test        # node --test（内置测试运行器）
npm run demo    # 进程1写检查点 -> 进程2模拟重启并恢复
```

演示脚本也可分步执行：

```bash
node examples/demo.ts write     # 进程 1：修改状态并生成 JSON 检查点
node examples/demo.ts restore   # 进程 2：加载检查点，增量恢复
```

## 设计

- `src/reactive.ts` — 响应式内核：`signal` / `computed` / `effect`，自动依赖追踪。
- `src/store.ts` — `Store`：信号注册表 + 检查点序列化 + 增量 Hydration。

### @persist 注解

Node 原生 type-stripping 不执行 TS 装饰器，因此 `@persist` 以等价的注解
API 提供：

```ts
import { Store, persist } from './src/store.ts';

const store = new Store();
const counter = persist(store, 'counter', 0);        // 等价于 @persist，会被序列化
const user = store.persisted('user', { name: 'a' }); // 同上
const session = store.signal('session', 'tmp');      // 临时状态，不序列化
```

### 检查点与恢复

- `store.checkpointToJSON()` / `store.saveCheckpoint(path)`：仅序列化
  `@persist` 信号，输出带 `version` / `createdAt` / `entries` 的 JSON。
- `store.hydrate(checkpoint)` / `store.hydrateFromFile(path)`：增量恢复——
  逐条写回持久化信号，每次写入都经过响应式图，派生 `computed`/`effect`
  自动重新对齐；随后所有临时信号安全重置为初始值。返回
  `HydrationReport`（`applied` / `skipped` / `resetTransient` /
  `effectsReAligned`）。
- 检查点中的未知 key 与临时信号 key 会被安全跳过，无法越权覆盖临时状态。

## 验收边界覆盖（test/store.test.ts）

- 内存混合临时/持久化状态，检查点只含 `@persist` 信号；
- 模拟进程重启：新建 `Store` 加载检查点，持久化状态完美复原；
- 依赖链（`computed` + `effect`）在恢复后自动重新求值；
- 临时状态安全重置，且检查点无法注入临时信号；
- 检查点文件的磁盘往返与版本校验。
