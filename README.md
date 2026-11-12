# 敏感异常对象脱敏

服务把异常对象传给日志时，嵌套cause和自定义属性可能泄漏令牌。写可导入的TypeScript异常快照包，只读取自有数据属性，禁止执行getter或toJSON；cause图可共享和循环，按字段策略删除或替换秘密，保留无歧义的引用编号。总节点和文本预算有界，用恶意访问器与循环异常测试输出不泄漏且原对象不变。

## 用法

```ts
import { snapshotError, snapshotToJson } from "./index.ts";

try {
  await charge(customer);
} catch (err) {
  logger.error(snapshotToJson(err, {
    fieldPolicy: { sessionId: "remove" },   // 逐字段策略：删除
    redactKeys: ["tenantSecret"],           // 追加敏感字段：默认替换
    maxNodes: 200, maxStringLength: 1024,   // 节点与文本预算
  }));
}
```

## 快照格式

- 对象节点：`{ $id, $type, props }`；数组节点额外含 `items`；用户键只出现在
  `props`/`items` 内，`$` 前缀键仅用于包装层，引用编号无歧义。
- 重复/循环引用：`{ $ref: id }`，指向首次出现节点的 `$id`。
- 访问器属性输出 `[omitted: accessor]`，绝不调用；唯一例外是引擎内置的
  `Error#stack` getter（按函数同一性校验后安全调用）。
- 命中敏感字段（内置列表 + `redactKeys`，大小写不敏感）按策略替换为
  `[REDACTED]` 或整体删除。

## 测试

```sh
npm test
```
