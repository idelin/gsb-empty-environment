# CFG Taint Analyzer

针对控制流图（CFG）的路径敏感污点分析器，TypeScript 实现（零依赖）。
输入为描述 CFG 的 JSON，输出从**来源（source）到危险调用（sink）的最短可行路径**；
在有限布尔条件下自动排除互相矛盾的分支。

## 运行

需要 Node.js ≥ 22.6（利用内置的类型擦除直接运行 TypeScript）。

```bash
npm run analyze -- examples/basic.json          # 人类可读输出
npm run analyze -- examples/basic.json --json   # 机器可读输出
npm test                                        # 运行测试
```

退出码：发现污点流为 `1`，无流为 `0`，输入非法为 `2`（便于接入 CI 卡口）。
如需类型检查：`npm i -D typescript @types/node` 后执行 `npm run typecheck`。

## 图模型

```jsonc
{
  "name": "demo",
  "entry": "n1",
  "nodes": [ /* 见下 */ ],
  "edges": [
    { "from": "n1", "to": "n2" },
    { "from": "n2", "to": "n3", "cond": { "var": "flag", "value": true } }
  ]
}
```

节点类型（`kind`）：

| kind       | 字段                          | 语义 |
|------------|-------------------------------|------|
| `source`   | `var`, `taints[]`             | 引入污点：`var` 带上指定的污点类型集合 |
| `const`    | `var`                         | 写入常量：清除 `var` 的全部污点 |
| `assign`   | `dst`, `src`                  | 赋值：`dst` 继承 `src` 的污点 |
| `alias`    | `dst`, `src`                  | 取别名：`dst := &src` |
| `sanitize` | `dst`, `src`, `types[]`       | 净化：仅移除列出的污点类型 |
| `boolSet`  | `var`, `value`                | 设置布尔条件变量 |
| `sink`     | `var`, `accepts[]`, `label?`  | 危险调用：`var` 的污点与 `accepts` 相交即触发 |
| `branch`   | —                             | 纯控制流分叉，条件标在出边上 |
| `nop`      | —                             | 空操作 |

## 分析规则

- **赋值**：污点随值复制；常量赋值清空污点。
- **合并**：采用路径敏感策略——不同前驱的污点环境作为独立状态并存，
  等价于在汇合点取并集；仅当同一节点上已存在**布尔约束与别名结构相同、
  且污点逐变量为超集**的状态时才剪枝（转移函数单调，剪枝不丢可达性）。
- **净化**：`sanitize` 是类型导向的，只移除 `types` 列出的污点类型，
  其余类型原样保留（例如只净化 `xss` 后 `sql` 仍可触发 `db.query`）。
- **别名写入**：`alias` 建立引用关系后，对别名的写操作会穿透到被引用变量，
  因此 `*p = tainted` 可以让此前已净化的 `a` 重新被污染（净化失效）。
  读取别名同样穿透到目标变量。
- **危险调用**：`var` 的污点集合与 `accepts` 相交非空即构成污点流。
- **布尔可行性**：沿路径收集 `cond` 约束；若某条出边要求的条件与已收集
  约束矛盾（如已确定 `flag = true`，而边要求 `flag == false`），该分支
  在此路径上不可行，直接排除。`boolSet` 会覆盖对应变量的约束。

## 最短路径

分析器对状态 `(节点, 污点环境, 布尔约束, 别名结构)` 做 BFS，
首次命中被污染的 sink 时即为**最短可行路径**；报告的路径会裁剪掉
入口到来源节点之间无关的前缀，并附上路径所依赖的布尔约束。

## 示例

- `examples/basic.json` — 基本赋值传播
- `examples/sanitize-types.json` — 净化仅对指定类型有效
- `examples/alias-invalidation.json` — 别名写入使净化失效
- `examples/branch-contradiction.json` — 矛盾分支被排除（报告 NO FLOW）
- `examples/shortest-path.json` — 两条可行流中取最短

## 代码结构

- `src/types.ts` — CFG 模式定义
- `src/parser.ts` — JSON 校验与索引构建
- `src/analyzer.ts` — BFS 污点分析核心
- `src/cli.ts` — 命令行入口
- `test/analyzer.test.ts` — 基于 `node:test` 的规则测试
