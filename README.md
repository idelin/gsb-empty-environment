# protobuf-wire-inspector

零外部依赖的 Protocol Buffers 二进制 Wire-format 编解码器 + 形式化约束巡检与不变式断言仪（TypeScript 版）。

## 特性

**Wire-format 编解码（`src/`）**
- Varint 7 位压缩编码（64 位精度走 BigInt，超 10 字节 / 溢出即拒绝）
- ZigZag 有符号整数转换（sint32 / sint64）
- Fixed32 / Fixed64 / SFixed / Float / Double 小端定长
- Length-delimited 嵌套消息（深度限制防栈耗尽）
- Packed Repeated 数组（解码端同时兼容 packed 与 unpacked 两种形态）
- 未知字段保留并可无损回写
- 安全越界防护：任何截断都以 `TruncationError`（携带 offset/needed/available）失败，绝不越界读取；bytes 一律返回拷贝，不悬挂引用输入缓冲

**完整性巡检引擎（`src/inspector.ts`）**
- 悬挂引用：指向不存在实体的边，精确定位来源字段
- 孤立环路：迭代式 Tarjan SCC，找出从任何根不可达的环（含自环）
- 数据倾斜：入度分布热点（max/mean 比率阈值）+ Gini 系数
- 不变式断言：注册全量状态谓词，`inspect()` 出报告，`assertClean()` 违规即抛 `InvariantViolationError`
- 纯函数式遍历，不修改图，可并发安全运行

## 运行要求

Node.js >= 23.6（原生 TypeScript 类型擦除，零构建、零依赖）。

```bash
npm test    # node --expose-gc --test "test/*.test.ts"
npm run demo
```

## 使用示例

```ts
import { decodeMessage, encodeMessage, EntityGraph, IntegrityInspector } from './src/index.ts';

const Order = {
  typeName: 'Order',
  fields: [
    { name: 'id', number: 1, kind: 'uint64', id: true },
    { name: 'userId', number: 2, kind: 'uint64', ref: 'User' },
    { name: 'amounts', number: 3, kind: 'sint32', repeated: true, packed: true },
  ],
} as const;

const buf = encodeMessage(Order, { id: 1n, userId: 42n, amounts: [10, -20] });
const msg = decodeMessage(Order, buf);          // 截断时抛 TruncationError

const graph = new EntityGraph();
graph.collect(Order, msg);
const report = new IntegrityInspector(graph).inspect();
```

完整可运行样例见 `examples/demo.ts`。

## 测试

34 项用例覆盖：varint/zigzag 全边界、各读取原语截断防护、多层嵌套 + packed 往返、
未知字段回写、深度限制、悬挂引用 / 孤立环路 / 数据倾斜 / 不变式、
200 路并发解码一致性、输入不可变性与堆增长有界（无泄漏断言）。

```
ℹ tests 34
ℹ pass 34
ℹ fail 0
```
