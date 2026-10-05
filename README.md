# 查询差异搜索器 (query-difference-searcher)

在**集合语义**下比较两个关系代数查询（仅含选择 σ、投影 π、并 ∪、差 −），
在有限属性域与有限总行数界内，搜索使两个查询结果不同的**最小数据库**。

- 先按**总行数最少**排序，再按**字典序**（数据库在全集行序下的有序指标列表）排序，
  返回第一个使两边结果不同的数据库；
- 若界内找不到反例，则报告"在给定界内等价"；
- 找到反例时，输出该数据库、两边各自的结果，以及每个差异元组的**来源行**
  （导致该元组出现的基表行，即 lineage）。

## 构建与运行

```sh
npm install        # 安装 typescript（开发依赖）
npm run build      # 编译到 dist/
npm test           # 编译并运行测试
node dist/src/cli.js examples/projection-difference.json
```

退出码：找到反例为 `1`，界内等价为 `0`，参数错误为 `2`。

## 规格文件格式（JSON）

```jsonc
{
  "domain": [0, 1],                 // 全局默认属性域（有限）
  "relations": [
    {
      "name": "R",
      "attrs": ["a", "b"],
      "domain": [0, 1],             // 可选：覆盖本关系全部属性的域
      "domains": { "a": [0, 1] }    // 可选：按属性覆盖
    }
  ],
  "maxRows": 4,                     // 数据库总行数上界
  "left":  { /* 查询 1 */ },
  "right": { /* 查询 2 */ }
}
```

查询 AST：

```jsonc
{ "kind": "relation",   "name": "R" }
{ "kind": "select",     "where": <谓词>, "input": <查询> }
{ "kind": "project",    "attrs": ["a"], "input": <查询> }
{ "kind": "union",      "left": <查询>, "right": <查询> }
{ "kind": "difference", "left": <查询>, "right": <查询> }
```

谓词：

```jsonc
{ "op": "eq|ne|lt|le|gt|ge", "left": <操作数>, "right": <操作数> }
{ "op": "and|or", "args": [<谓词>, ...] }
{ "op": "not", "arg": <谓词> }
// 操作数：{ "attr": "a" } 或 { "const": 0 }
```

∪ / − 的两个操作数以及左右两个顶层查询必须具有相同的输出属性集合，否则报错。

## 搜索顺序的精确定义

1. 全集 = 各关系（按声明顺序）属性域的笛卡尔积，元组按属性顺序、域按声明顺序字典序排列；
2. 数据库 = 全集的子集，先按子集大小（总行数）0,1,2,... 枚举；
3. 同样大小的子集按其有序指标列表的字典序枚举。

因此返回的反例在"总行数、字典序"序下是最小的。

## 代码结构

- `src/types.ts` — 查询 AST、谓词、模式等类型定义
- `src/eval.ts` — 集合语义求值（带 lineage），模式推断与校验
- `src/enumerate.ts` — 全集构造与数据库枚举（总行数 → 字典序）
- `src/search.ts` — 最小反例搜索
- `src/format.ts` — 结果格式化输出
- `src/cli.ts` — 命令行入口
- `examples/` — 两个示例：投影对差不分配（有反例）、选择对并分配（界内等价）
- `test/run-tests.ts` — 单元与集成测试

## 复杂度说明

界为 k、全集大小为 n 时，需检查 Σ_{i≤k} C(n,i) 个数据库，每个数据库上两次求值。
该工具面向小界（教学/调试场景，如域 {0,1}、2 属性 × 2 关系、maxRows ≤ 5）。
