# query-differ — 查询差异搜索器

在**集合语义**下比较两个仅含**选择(σ)、投影(π)、并集(∪)、差集(−)**的关系查询。
属性域有限、总行数有界,搜索器枚举所有可能的数据库,找出使两查询结果不同的
**最小反例数据库**;找不到则报告"给定界内等价"。

## 运行

需要 Node.js ≥ 22.6(原生 TypeScript 类型擦除,无需编译、无第三方依赖):

```sh
node src/cli.ts examples/project-minus.json      # 人类可读输出
node src/cli.ts --json examples/project-minus.json
cat spec.json | node src/cli.ts                  # 从标准输入读取
npm test                                         # 运行测试
```

退出码:`0` = 界内等价,`1` = 发现差异,`2` = 输入/运行错误。

## 最小性的定义

1. **全集**:每个关系的所有可能元组 = 各属性域的笛卡尔积;关系按声明顺序、
   元组按域值顺序(字典序)拼接成全集。
2. 数据库 = 全集的子集。枚举顺序:**先按总行数(0, 1, 2, … ≤ maxRows),
   同行数按元组索引组合的字典序**。
3. 第一个使 `Q1(D) ≠ Q2(D)` 的数据库即最小反例,搜索立即停止。

若 `maxRows ≥ 全集大小` 仍未发现差异,则等价性在该属性域内无条件成立
(输出中会标注"已穷举")。

## 输入格式(spec.json)

```jsonc
{
  "relations": [
    { "name": "R", "attributes": [
      { "name": "a", "domain": [0, 1] },     // 有限域,值可为数字/字符串/布尔
      { "name": "b", "domain": [0, 1] }
    ]}
  ],
  "maxRows": 4,                               // 总行数上界
  "queries": { "Q1": { ... }, "Q2": { ... } } // 查询 AST,见下
}
```

查询 AST(递归):

| 算子 | JSON |
|---|---|
| 关系 | `{ "op": "rel", "name": "R" }` |
| 选择 | `{ "op": "select", "input": Q, "predicate": P }` |
| 投影 | `{ "op": "project", "input": Q, "attributes": ["a"] }` |
| 并集 | `{ "op": "union", "left": Q, "right": Q }` |
| 差集 | `{ "op": "minus", "left": Q, "right": Q }` |

谓词:

```jsonc
{ "kind": "cmp", "attr": "a", "cmp": "eq", "value": 1 }   // 与常量比较
{ "kind": "cmp", "attr": "a", "cmp": "lt", "attr2": "b" } // 与另一属性比较
{ "kind": "and", "args": [P, ...] }  // 或 "or"
{ "kind": "not", "arg": P }
```

`cmp` 取值:`eq | ne | lt | le | gt | ge`。并/差两边属性列表必须一致,
否则报"模式不兼容"错误;两查询的**结果**模式不同则空数据库即构成差异。

## 输出

**发现差异时**:最小反例数据库(各关系的行)、Q1/Q2 两边结果、
仅在 Q1 / 仅在 Q2 的差异行,以及每行的**来源行**。

来源行采用翻转敏感性定义:对全集中某一行切换"在/不在数据库"状态,
若差异行在 Q1 或 Q2 结果中的成员资格因此改变,则该行是来源行。
输出会标注该行是否在数据库中、影响哪一侧查询。

**界内等价时**:报告 maxRows、已检查数据库数,以及是否已穷举整个域。

## 保护开关

枚举量为 `Σ C(全集大小, k)`。超过上限(默认 2,000,000)时拒绝执行,
可缩小属性域 / `maxRows`,或用 `--max-databases N` 放宽。

## 代码结构

- `src/types.ts` — spec / 查询 AST / 数据库类型
- `src/evaluate.ts` — 集合语义求值器(去重基于集合)
- `src/enumerate.ts` — 全集构造与按 (行数, 字典序) 的子集枚举
- `src/search.ts` — 最小反例搜索与等价判定
- `src/lineage.ts` — 来源行(翻转敏感性)
- `src/cli.ts` — 命令行入口
- `test/differ.test.ts` — 经典恒等式与反例的回归测试

如需静态类型检查:`npm i -D typescript @types/node && npx tsc --noEmit`。
