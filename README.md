# 模板原型污染隔离

可导入的 TypeScript 安全配置合并组件。不可信配置只能以 JSON 文本进入，
全程在无原型隔离树中合并，成功后才一次性发布，模板运行时的全局原型与旧
配置不会被污染或半更新。

## 安全模型

- **仅接受 JSON 文本解析出的树**：`ParsedTree.parse` / `parseJsonTree`
  是唯一入口，返回带品牌的 `ParsedTree`，普通对象无法伪装成解析结果传给
  合并器。
- **无原型记录**：所有对象节点用 `Object.create(null)` 创建，`__proto__`
  等键在解析期就是普通自有属性，无法触碰任何原型。
- **危险路径段拒绝**：合并期对任意深度的 `__proto__`、`constructor`、
  `prototype` 键直接抛 `MergeError`（见 `DANGEROUS_KEYS`）。
- **重复 JSON 键拒绝**：手写递归下降解析器，对象内重复键立即抛
  `JsonParseError`，杜绝“后者覆盖”式污染。
- **深度上限**：`maxDepth`（默认 32）限制容器嵌套，防止栈耗尽攻击。
- **数组按索引替换**：同路径数组按整数下标逐元素合并、保留尾部元素；
  非数组值整体替换；可用 `indexReplaceArrays: false` 改为整组替换。
- **隔离构建后发布**：`ConfigStore.publish` 先在全新草稿树上完成全部合并，
  任一来源失败则旧配置引用与内容完全不变；成功后单次切换引用，结果树
  递归冻结。
- **叶子来源路径**：每个标量叶子在 `origins`/`provenance` 中以 RFC 6901
  JSON Pointer 为键，记录最后写入它的来源名称。

## 用法

```ts
import { ConfigStore, parseJsonTree } from './index.ts';

const store = new ConfigStore();
store.publish([
  { tree: parseJsonTree('{"service":"tpl","timeout":30}', { sourceName: 'base.json' }) },
]);

try {
  store.publish([
    { tree: parseJsonTree('{"timeout":60}', { sourceName: 'override.json' }) },
    { tree: parseJsonTree('{"__proto__":{"polluted":true}}', { sourceName: 'attack' }) },
  ]);
} catch (error) {
  // 合并整体失败，store.config 仍是上一份已发布配置
}

store.getOrigin('/timeout'); // 来源名称，或 "<previous-config>"
```

无状态合并可用 `mergeConfigs(sources)`，只做校验可使用
`store.buildInIsolation(sources)`（草稿不发布）。

## 测试

```sh
npm test
```

覆盖：嵌套特殊键攻击、全局 `Object.prototype`/`Array.prototype` 前后快照、
稀疏数组替身（类数组记录与真实数组的索引替换）、深度上限、重复键、失败
发布后旧配置引用与来源映射不变性。
