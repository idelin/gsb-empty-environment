# 连接顺序查询计划器（Join Order Optimizer）

基于子集动态规划（DPsize 风格）的查询计划器：接收有理数基数与连接选择率，
枚举**所有二叉连接树**（bushy，不限左深树），以精确有理数计算成本，输出
各节点估算与最低成本计划。最多支持 **12 张表**。

## 运行

需要 Node.js ≥ 22.6（推荐 23.6+，可原生运行 TypeScript）：

```bash
node src/cli.ts examples/sample.json            # 文本输出
node src/cli.ts examples/sample.json --dump-dp  # 附带所有子计划估算（DP 表）
node src/cli.ts examples/sample.json --json     # JSON 输出
cat examples/sample.json | node src/cli.ts -    # 从标准输入读取
node --test test/optimizer.test.ts              # 运行测试
```

## 输入格式

```json
{
  "tables": [{ "name": "A", "cardinality": "1000" }],
  "edges": [{ "tables": ["A", "B"], "selectivity": "1/100" }],
  "allowCrossProducts": false,
  "materialization": { "perRow": "1/20", "fixed": "0", "includeRoot": false }
}
```

- `tables[].cardinality`：基表基数，有理数（支持 `"3/4"`、`"0.75"`、`"1e6"`）。
- `edges[].selectivity`：等值连接选择率；未列出的表对之间无谓词（选择率 1）。
- `allowCrossProducts`：`false` 时禁止任何含笛卡尔积的子计划（默认 `true`）。
- `materialization`：可选。中间结果物化代价 = `perRow × 基数 + fixed`；
  默认不计根节点（最终结果），可用 `includeRoot` 打开。缺省表示不物化。

## 成本模型

- 子集 `S` 的基数：`∏ 基表基数 × ∏ S 内部所有谓词选择率`。
- 连接步成本 = 输出基数 + 物化代价；计划总成本 = 所有内部节点步成本之和。
- 所有数值用 BigInt 分数精确运算，无浮点误差。

## 输出

- 最优计划树：每个连接节点的表集合、基数估计、选择率、本步成本、
  物化代价、累计成本。
- 最优总成本（精确分数 + 十进制近似）与枚举统计。
- `--dump-dp`：每个可达子计划（DP 状态）的基数与最优累计成本。

## 结构

- `src/rational.ts`：BigInt 精确有理数。
- `src/optimizer.ts`：子集 DP 稠密枚举核心（`optimize()`，可作为库导入）。
- `src/cli.ts`：命令行入口。
- `test/optimizer.test.ts`：单元测试（含手算成本、bushy 优于左深等用例）。
