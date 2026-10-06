# range-mapping

两轮源码转换的段映射组合库（TypeScript）。

## 模型

- 每轮转换产生一个 **Mapping**：一组按输出坐标排序、互不重叠的 **Segment**。
- 每个 Segment 把输出的半开范围 `[outStart, outEnd)` 映射到输入的半开范围
  `[srcStart, srcEnd)`：
  - **映射段**：`srcEnd - srcStart === outEnd - outStart > 0`（保留/复制的文本）；
  - **插入段**：`srcStart === srcEnd`（零宽锚点），`outEnd > outStart`。
- 被删除的源文本不出现于任何段（即"空隙"）。

## API（`src/mapping.ts`）

- `compose(first, second)`：组合 S0→S1 与 S1→S2 两轮映射，得到 S0→S2。
  插入锚点经 `anchorInSource` 翻译到原始输入。
- `querySource(mapping, q0, q1)`：正向查询输出范围的来源，按输出顺序返回
  `source` 片段与 `insertion` 锚点。相邻 source 片段仅在源中连续时合并——
  删除空隙会阻止合并，零宽插入不会。
- `queryOutputs(mapping, s0, s1)`：反向查询源范围在输出中的位置，多值
  （复制的源文本每次出现各返回一个范围）。
- `validateMapping` / `isInsertion` / `anchorInSource`：辅助函数。

## 验证

`test/run-tests.mjs` 用逐字符带来源标签（`{s0, s1}`，`null` 表示该轮插入）
的模型作为基准：从标签推导每轮映射，用库组合后，把正向/反向查询结果与
标签逐项核对。包含替换、复制、纯插入的确定性用例，以及 2000 轮随机
（插入/替换/删除/复制）模糊测试。

```sh
npm test        # 构建（scripts/build.mjs）并运行全部测试
```

环境无网络时，`scripts/build.mjs` 用类型擦除把 `src/mapping.ts` 编译为
`dist/mapping.mjs`（源码只使用可擦除语法）。有网络时可直接使用真实编译器：

```sh
npx tsc -p tsconfig.json
```
