# 差分结果结构对齐（netsec-uid1242）

可导入的 TypeScript 差分判定包：对同一份输入运行两个协议解析回调，裁决两个实现之间的**语义分歧**，并把顺序差异、错误位置差异、等值异编码等噪声与真正的语义分歧区分开。

## 安装与导入

零依赖，面向 Node ≥ 23.9（原生类型擦除，无需构建）。

```ts
import { compare } from "./index.ts";
import type { ParserCallback, ComparisonResult } from "./index.ts";
```

## 解析回调契约

调用方提供两个 `ParserCallback`，把各自实现的结果归一化为 `ParseOutcome`：

```ts
const parserA: ParserCallback = (input) => {
  // 接受：消费字节数 + 绑定字节区间的类型树
  return {
    accepted: true,
    consumed: 2,
    tree: {
      type: "Message",
      range: { start: 0, end: 2 },
      children: [
        { name: "tag", node: { type: "VarInt", value: 300n, range: { start: 0, end: 2 }, encoding: "canonical" } },
      ],
    },
  };
  // 拒绝：统一错误类别（可选位置、部分树、已消费字节数）
  // return { accepted: false, error: { category: "truncated", range: { start: 1, end: 2 } }, consumed: 1 };
};

const result: ComparisonResult = compare(input, parserA, parserB);
```

回调抛异常会被捕获并归一化为 `internal` 类别的拒绝，不会中断比较。

## 判定规则

裁决按以下顺序短路，**接受与拒绝永远不归并**：

1. 一边接受、一边拒绝 → `acceptance_mismatch`（semantic），证据只落在拒绝侧的错误上。
2. 双方都拒绝：
   - 错误类别不同 → `reject_category_mismatch`（semantic）；
   - 错误类别相同、位置不同 → `error_location_mismatch`（informational，不判语义分歧）。
3. 双方都接受：先比较 `consumed`（`consumed_length_mismatch`），再做类型树差分。

树差分规则：

- **最小分歧路径**：某节点一旦产生分歧即停止向下展开；`divergences[].path` 给出从根到该节点的字段名/下标/类型，两侧证据各自携带原始 `range`、`encoding`、`value`。
- **顺序**：复合节点默认有序，按位置比较；同元素换序报 `order_mismatch`。只有两侧都显式 `ordered === false` 才按语义指纹做无序多重集匹配，重排后节点仍携带各自原始字节区间，且重排层不产生 `range_mismatch`。
- **重复字段**：有序容器按位置配对；无序容器按深度指纹做多集配对，基数不同逐个报 `cardinality_mismatch`，单侧证据为 `null`。
- **等值不同编码**：语义值相等但 `encoding` 不同 → `encoding_mismatch`（informational）；字节区间不同 → `range_mismatch`（informational）。两者都不影响 `equivalent`。
- 类型不同 / 叶子语义值不同 / 数量不同分别报 `type_mismatch`、`value_mismatch`、`cardinality_mismatch`（均 semantic）。

`ComparisonResult.equivalent` 仅在不存在任何 `semantic === true` 的分歧时为真。

## 结果结构

- `left` / `right`：`accepted`、`consumed`、`error` 摘要。
- `trees`：双方类型树（拒绝侧可能为部分树或 `null`）。
- `divergences`：最小分歧列表，每条含 `category`、`severity`、`semantic`、`path`、`left`/`right` 证据与 `detail`。

## 测试

```sh
npm test
```

`test/diff.test.ts` 覆盖：重复字段（一致/值分歧/数量分歧）、有序与无序重排、无序多重集基数、截断（接受 vs 拒绝、部分树）、双侧拒绝的类别与位置区分、回调异常归一化、消费字节数差异、等值不同编码与区间差异，以及真实 varint 严格/宽松解析回调的端到端比较。

## 模块布局

- `src/types.ts`：公共类型（`TypeNode`、`ParseOutcome`、`Divergence` 等）。
- `src/value.ts`：语义等值与稳定指纹（支持 bigint、Uint8Array、Map/Set、对象键序无关）。
- `src/diff.ts`：类型树差分引擎（最小路径、无序多重集、证据绑定）。
- `src/compare.ts`：顶层裁决与 `compare()` 入口。
- `index.ts`：包导出入口。
