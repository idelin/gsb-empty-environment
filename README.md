# 隔离领域函数边界

宿主通过 ShadowRealm 式的隔离接口执行不可信脚本。脚本领域回传的函数若直接暴露，
调用方可经 `.constructor` 拿到该领域的 `Function` 构造器，进而编译执行任意代码逃出。
本组件是一个可导入的 TypeScript 调用边界（membrane），把跨界值严格收敛为
**原始值**与**硬化包装函数**两类。

## 用法

```ts
import { createBoundary, BoundaryError } from "./index.ts";
import vm from "node:vm";

const boundary = createBoundary();
const guestContext = vm.createContext({});
const rawGuestFn = vm.runInContext(
  "(function () { return function (x) { return x + 1; }; })()",
  guestContext,
);

// 不可信函数必须经过边界包装后才能在宿主侧调用
const safe = boundary.wrapCallable(rawGuestFn);
safe(41); // 42
safe({}); // 抛 BoundaryError：对象不允许跨界
```

宿主回调反向注入脚本领域时同样使用 `wrapCallable`，两个方向共用同一份过滤规则
和包装缓存（同一原始函数始终映射到同一个包装，不会二次包装）。

## 边界规则

- **原始值**（`string` / `number` / `bigint` / `boolean` / `symbol` / `null` /
  `undefined`）原样通过；`symbol` 是原始值，`Symbol.for(...)` 跨领域身份保持一致。
- **可调用值**（含跨 realm 函数与 callable proxy）包成硬化函数：
  - `Object.getPrototypeOf(wrapper) === null`，因此没有 `constructor` 属性，
    也没有 `__proto__`、`call`、`apply`、`bind` 等 `Function.prototype` 方法；
  - 不可扩展且被冻结，无法补写 `constructor`；
  - 以箭头函数实现、无 `prototype` 属性，`new` / `Reflect.construct` 直接 `TypeError`；
  - 调用时以 `undefined` 为 `this`，`this` 不跨界。
- **其它对象**（普通对象、数组、`Error`、`Date`、装箱原始值、`Object.create(null)`
  等）一律拒绝，无论出现在参数还是返回值位置。
- 参数与返回值递归过滤，因此嵌套回传的函数也会被自动包装。
- 内部抛出的任何值（自定义对象、Error、函数、原始值、null）统一规范化为
  宿主领域的 `BoundaryError`；规范化过程只读取 `typeof`，不访问 `message` /
  `name` 等任何属性，不挂载 `cause`，原始抛出对象绝不离开边界。

## 为什么 getter 副作用不会触发

边界判断只用 `typeof`、`WeakMap` / `WeakSet` 的键查找和 `Reflect.apply`，
不枚举、不读取、不强制转换目标对象的任何属性，因此恶意定义在
`constructor` 或其它键上的访问器、以及 proxy 的 `get` 陷阱都不会在过滤阶段被触发。

## 测试

`test/boundary.test.ts` 使用 `node:vm` 构造相互隔离的领域（独立的
`Object` / `Function` / `Symbol` 内建对象），覆盖：

1. constructor 链切断（并先证明未防护原始函数确实可经 `constructor` 编译代码）；
2. 对象/数组/无原型对象在返回值与参数两个方向被拒绝；
3. 访问器与 proxy `get` 陷阱在过滤、抛错路径上零副作用；
4. Symbol 原始值、全局 symbol 注册表、领域内建 `Symbol` 的包装与装箱对象拒绝；
5. 抛出自定义对象、Error、函数、null、原始值时的统一错误规范化；
6. 两个 vm 领域与宿主之间的双向过滤、身份缓存与 `this` 隔离。

```sh
npm test
# 等价于 node --experimental-strip-types --test
```
