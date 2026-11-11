# 插件消息对象图隔离

运行于 Node.js 的安全插件宿主在向不可信子进程发送消息前，必须保证能力句柄（capability handle）不会随对象图意外泄漏。本包提供可导入的 TypeScript 结构化复制器：先完整校验并复制整棵对象图，全部成功后才调用发送回调。

## 安全模型

复制过程从不执行输入中的任何方法，校验顺序固定为：

1. 原始值直接放行并计入字节预算（`string`、`number`、`bigint`、`boolean`、`null`、`undefined`）。
2. 对对象先调用 `node:util` 的 `util.types.isProxy` 识别并拒绝代理，发生在任何属性访问之前，因此代理陷阱不会被触发（已撤销代理同样被拒）。
3. 通过 `CapabilityRegistry` 拒绝宿主注册过的能力对象，函数形式的能力在被当作函数拒绝之前先做能力比对。
4. 只允许数组与原型为 `Object.prototype` 或 `null` 的数据属性记录；`Date`、`Map`、`RegExp`、类实例、函数等一律拒绝。
5. 使用 `Reflect.ownKeys` 加 `Object.getOwnPropertyDescriptor` 检查每个属性：访问器（get/set）、不可枚举属性、符号键、数组非索引属性均拒绝；描述符检查不读取属性值，因此 getter 永不执行。

共享子图通过内部 `Map` 保留别名，循环引用允许且以迭代方式遍历（无递归栈溢出风险）。节点数与字节数在遍历过程中实时扣减，超限即抛出 `BudgetExceededError`，已分配的半成品副本不会被发送。

## 安装与导入

```ts
import {
  CapabilityRegistry,
  SecureMessenger,
  copyMessage,
} from 'secure-message-copy';
```

包以 ESM TypeScript 源码发布，Node.js ≥ 22.18 可直接运行（原生类型擦除），相对导入均带 `.ts` 扩展名。

## 使用示例

```ts
import { CapabilityRegistry, SecureMessenger } from 'secure-message-copy';

const registry = new CapabilityRegistry();
const spawnHandle = { spawn() { /* 宿主特权操作 */ } };
registry.register(spawnHandle);

const messenger = new SecureMessenger({
  registry,
  budgets: { maxNodes: 10_000, maxBytes: 1_048_576 },
});

// 复制全部成功后 sendCallback 才会被调用一次；任何校验失败则零调用。
messenger.send(
  { command: 'ping', args: [1, 2, 3] },
  (safeMessage) => childProcess.send(safeMessage),
);
```

也可直接使用 `copyMessage(value, { registry, budgets })` 取得深拷贝。

## API

- `copyMessage<T>(value, options?): T`：返回与输入别名结构一致的深拷贝；失败抛错且无副作用。
- `SecureMessenger`：持有注册表与预算，`send(message, callback)` 仅在完整复制成功后以副本调用回调；`copy(message)` 只返回复制结果。
- `CapabilityRegistry`：`register(handle)`、`unregister(handle)`、`has(handle)`；注册代理会被拒绝。
- 预算：`maxNodes` 限制对象/数组节点数（共享与循环节点只计一次），`maxBytes` 限制原始值、键与节点开销的累计字节数；默认 `10_000` 节点、`1 MiB`。

所有错误继承自 `CopyError`：`ProxyRejectedError`、`CapabilityLeakError`、`AccessorRejectedError`、`UnsupportedValueError`、`BudgetExceededError`。

## 测试

```sh
npm test          # node --test，内置测试运行器 + 类型擦除
npm run typecheck # tsc --noEmit（需本机安装 TypeScript）
```

测试覆盖：带陷阱代理的零陷阱调用与零发送、循环引用与别名保留、能力句柄（对象与函数）泄漏拦截、访问器/方法零执行、节点与字节预算超限（含循环图）以及失败时发送回调零调用。
