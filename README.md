# compressed-validation-errors

把大量相似的校验错误压缩成摘要（连续数组下标压成区间），同时保留可展开的
原始路径。TypeScript 编写，仅用 Node.js 内置模块（`node:test`、`node:crypto`），
Node >= 23 直接运行 `.ts`（类型擦除），无第三方依赖。

## 运行

```sh
npm test       # node --test，37 个测试
npm run sample # 运行样例，打印压缩效果与空间节省
```

## 错误模型

每条错误同时携带实例路径与规则路径，并标记 schema 版本：

```ts
interface ValidationError {
  schemaVersion: string;        // 如 "orders/v1"，不同版本绝不合并
  rule: string;                 // 如 "minimum"、"required"
  instancePath: (string|number)[]; // 如 ["items", 7, "qty"]
  schemaPath: string;           // 如 "#/properties/items/items/properties/qty/minimum"
  message: string;
  params?: Record<string, unknown>;
}
```

## 校验器子集（只承诺这些）

`src/validator.ts` 为手写校验器，不借用现成验证器。仅支持：
`type`、`properties`、`required`、`items`、`additionalProperties`(布尔)、
`enum`、`const`、`minimum`、`maximum`、`minLength`、`maxLength`、
`minItems`、`maxItems`。其它关键字一律不承诺。

## 压缩模型

- 分组键 = `schemaVersion | rule | schemaPath | 路径模板 | message | params`，
  不同规则/原因/版本永远不会合并。
- 路径模板把最后一个数字段（数组下标）换成槽位；同组内用区间
  `[start, end, mult]` 记录出现的下标及多重次数（同路径多错误靠 mult 保留）。
- 区间始终规范化：有序、不重叠、相邻且 mult 相同必合并。
  因此 1..100 全失败是 `[[1,100,1]]`；缺第 50 项则是
  `[[1,49,1],[51,100,1]]`，绝不会误写成连续。
- 无数字段的路径也能分组（slot = -1），多重性照样保留。

## 在线增删与守恒

`ErrorIndex.addError / retractError` 只动命中的那个分组（区间分裂/合并），
不重建全量索引。守恒不变量：`totalCount === 各组 count 之和 === expand().length`，
差分测试在每次随机操作后都校验。

## 展开

`expand(filter)` 支持按 `rule`、`schemaVersion`、`pathPrefix`
（可精确到某个数组下标，如 `/items/7`）过滤，以及 `order: "original" | "path"`。
原始顺序按分组首次出现顺序 + 组内下标升序。解压结果与原错误集合（含重复）
严格相等，测试用多重集对比验证。

## 序列化与防伪

`serialize()` 产出带 `format` 版本号和 SHA-256 校验和的 JSON。
`deserialize()` 做两层防御：

1. 校验和：任何对计数/区间的篡改都会被发现；
2. 结构校验（即使攻击者重算校验和）：区间必须 `start <= end`、`mult >= 1`、
   有序不重叠、相邻同 mult 必已合并；组 `count` 必须等于区间展开数；
   `totalCount` 必须等于各组之和。

## 测试

- `test/ranges.ts` 级别：区间压缩/分裂/多重性。
- `test/error-index.test.ts`：分组隔离、在线增删、按规则/前缀/原序展开、守恒。
- `test/differential.test.ts`：3000 次随机增删操作，对照独立完整错误列表
  （参考多重集），每步校验计数守恒，周期性全量比对展开结果与过滤展开。
- `test/serialization.test.ts`：往返一致性；篡改计数/区间被校验和拦截；
  重签名的非法区间被结构校验拦截；实测空间节省。
- `test/validator.test.ts`：子集行为、实例路径与规则路径。
