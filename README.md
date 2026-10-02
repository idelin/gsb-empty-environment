# protobuf-wire-inspector

零外部依赖的 Protocol Buffers 二进制 Wire-format 编解码器 + 反射模式校验引擎
(形式化约束巡检与不变式断言仪),TypeScript 实现,Node.js 内置测试运行器验证。

## 组件

- `src/wire.ts` — 底层原语:Varint(7 位压缩)、ZigZag 有符号转换、Fixed32/64(小端),
  以及 `TruncationError` 安全越界防护(携带 offset/needed/available 元数据)。
- `src/reader.ts` / `src/writer.ts` — 边界检查游标与增量编码器;`subReader` 限定嵌套
  Length-delimited 子消息范围,`assertDrained()` 提供严格无泄漏断言。
- `src/schema.ts` — Schema 驱动的结构体编解码:嵌套消息、Packed Repeated 数组、
  未知字段安全跳过与统计。
- `src/inspector.ts` — 反射模式校验引擎:悬挂引用检测、孤立环路检测(Tarjan SCC)、
  数据倾斜量化(max/mean 比率)、泄漏汇总、用户注册的形式化不变式断言。
  引擎无可变共享状态,并发巡检结果逐位一致。

## 运行

需要 Node.js >= 22.6(利用内置 type-stripping,零依赖、零构建):

```bash
# 测试(node:test 内置运行器)
node --experimental-strip-types --test test/*.test.ts

# 运行样例
node --experimental-strip-types examples/demo.ts
```

## 验收边界覆盖

- 纯手工解析多层嵌套消息与 Packed Repeated 数组,精确还原结构体字段(含混合
  packed/unpacked 出现、未知字段跳过)。
- 字段截断(任意截断点、长度前缀越界、varint 溢出)全部抛出带防护元数据的
  `TruncationError`/`WireError`,绝不越界读取。
- 巡检引擎覆盖:悬挂引用、孤立环路(含自环)、孤立节点、数据倾斜、重复键、
  自定义不变式、解码泄漏;并发安全性由 64 路异步交错 + 4 个 worker 线程验证,
  断言严格无泄漏。
