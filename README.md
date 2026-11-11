# 污点查询上下文分离

审查服务发现同一个包装函数既处理受信配置又处理请求数据，合并摘要会产生大量误报。写可导入的TypeScript分析包，输入调用图和字段级污点转移，按最近两个调用点区分上下文，超过深度合并但必须保守；递归摘要迭代到稳定。输出来源与危险调用的上下文路径，用展开有限调用树的参考模型检查不漏报。

## 用法

```ts
import { analyze, referenceFindings, checkNoFalseNegatives, ap } from "./index.ts";

const program = {
  entry: "main",
  functions: [
    {
      name: "main",
      params: [],
      body: [
        { kind: "source", id: "user-input", target: ap("req", "body") },
        { kind: "call", site: "d1", callee: "dispatch", args: [ap("req")], results: ["t"] },
        { kind: "sink", id: "eval", args: [ap("t")] },
      ],
    },
    { name: "dispatch", params: ["x"], body: [{ kind: "return", values: [ap("x")] }] },
  ],
};

const result = analyze(program); // 默认按最近 2 个调用点区分上下文
for (const f of result.findings) {
  console.log(f.sinkId, f.sinkPath, f.sources);
}

// 参考模型：展开有限调用树，检查主分析不漏报
const check = checkNoFalseNegatives(result, referenceFindings(program, { maxDepth: 6 }));
console.assert(check.ok, check.missing);
```

运行测试：`npm test`（Node ≥ 22.6，使用 `--experimental-strip-types`，无第三方依赖）。

## 程序模型

- `Program`：命名函数集合 + 入口函数；函数体为直线式语句序列（无分支，合并语义由上下文 join 表达）。
- 语句：`source`（污点源）、`assign`（字段级赋值，访问路径如 `req.body`）、`sanitize`（清除该路径子树污点）、`call`（带调用点标签、实参、接收返回值的变量）、`return`、`sink`（危险调用）。
- 调用图由 `call` 语句的 `site`/`callee` 隐式给出；字段级污点转移由 `assign`/调用绑定/返回绑定表达。

## 分析语义（`src/analyze.ts`）

- **上下文敏感**：每个（函数, 上下文）实例即一份摘要，上下文为最近 `contextDepth`（默认 2）个调用点标签。超过深度的调用链被截断合并；合并对污点状态取并集，因此是保守上近似（只可能误报，不会漏报）。
- **字段敏感**：污点记录在访问路径上，读取时按前缀可比匹配；路径长度超过 `maxAccessPathDepth`（默认 2）时截断为祖先路径，等价于污染整棵子树（双向保守）。局部变量赋值做强更新，字段写入做弱更新。
- **不动点迭代**：工作list算法，输入污点沿调用边传播、返回污点沿摘要输出回传；递归函数的摘要（如 `rec@[rr,rr]` 自环）单调增长直至稳定。状态格有限，保证终止。
- **输出**：每条 finding 包含危险调用的函数、上下文、从入口出发的见证调用路径（`sinkPath`），以及每个来源的 id、函数、上下文与见证路径（`sources[].path`）。

## 参考模型（`src/reference.ts`）

`referenceFindings` 把调用图从入口展开成深度不超过 `maxDepth` 的有限调用树，每个树节点独立执行同一套语句语义（等于无限精度上下文），收集（来源， 危险调用）对。`checkNoFalseNegatives` 断言主分析覆盖参考模型的全部结果；测试中还对无上下文冲突的程序断言两者完全一致，并用随机程序生成器做属性测试。
