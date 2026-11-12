# 内置原型冻结锁定

插件宿主启动后，插件脚本仍可能通过 Object.prototype、Array.prototype 或 constructor.prototype 路径写入默认属性，影响后续所有请求。该组件冻结从 globalThis 可达的标准内置对象、静态方法、原型和访问器，并允许宿主把业务数据记录登记为可变例外。

## 使用

```ts
import {
  lockBuiltins,
  registerMutableRecord
} from "./index.ts";

// 宿主数据保持可变；对象和路径两种登记方式都可以。
const hostCache = registerMutableRecord({ entries: [] });
const coverage = lockBuiltins({
  mutableRecords: [hostCache],
  exceptionPaths: ["globalThis.hostConfig"]
});

console.log(coverage.includes("globalThis.Object.prototype"));
console.log(coverage.skippedPaths);
```

`lockBuiltins` 返回一个冻结的路径数组，同时提供 `coveredPaths`、`skippedPaths`、`mutableRecords`、`failures` 和 `complete` 等非枚举诊断字段。也可以使用 `freezeBuiltins`、`lockBuiltinPrototypes` 等别名，或使用 `BuiltinPrototypeLocker` 保存宿主例外。

默认只遍历 ECMAScript 和常见 WHATWG 标准全局对象，不会冻结 `process`、`console`、Node 模块等宿主设施。设置 `includeHostGlobals: true` 可扩大遍历范围；此时仍应通过 `mutableRecords` 或 `exceptionPaths` 排除宿主数据。

## 遍历与例外

- 使用 `Reflect.ownKeys` 遍历自有字符串键和符号键，不调用原型上的 getter。
- 保留访问器属性，并冻结其 getter/setter 函数。
- 跟随函数自己的 `prototype`，也沿 `Object.getPrototypeOf` 覆盖 `%TypedArray%.prototype` 这类没有全局名字的内建原型。
- 宿主记录和图路径会整体跳过；若记录挂在 globalThis 上，`skippedPaths` 会记录对应路径。
- `Error.prototype.name/message` 使用冻结的访问器实现，实例仍可创建自己的 `name`/`message`，但改写原型仍会抛出 `TypeError`。

测试覆盖严格模式写入、`Object.setPrototypeOf`、JSON 解析出的 `__proto__` 合并，以及宿主例外保持可写。
