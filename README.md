# 异常路径资源所有权

扩展宿主要求插件不得把已经关闭的安全通道传给回调。用TypeScript实现供编译插件调用的所有权分析包，输入带正常边和异常边的过程图，操作含获取、移动、借用、归还、关闭及延迟清理；借用不得越过关闭，移动后原变量失效。支持循环不动点并给出违规路径。用嵌套清理、部分初始化失败和异常重抛的图测试，比较小图所有执行序列。

## 包结构

- `src/types.ts`：过程图（节点/正常边/异常边）、操作、违规与见证路径类型。
- `src/state.ts`：抽象状态（变量状态幂集格、借用句柄、region 栈与延迟清理表）及 join/clone/相等判定。
- `src/transfer.ts`：节点与边的转移语义，抽象分析与具体枚举共用同一份实现。
- `src/builder.ts`：`GraphBuilder` 构图；在 `build()` 沿正常边做静态 DFS，自动为异常边计算 LIFO 展开序列（含重抛时只展开剩余外层 region）。
- `src/analyzer.ts`：worklist 单调数据流分析，有限格保证循环回边有限次迭代到达不动点；每条违规附从入口到违规点的可执行见证路径。
- `src/enumerate.ts`：小图具体解释器，DFS 枚举所有执行序列（带步数上限），用于与不动点分析交叉验证。

## 语义要点

- 变量状态：`owned / moved / closed`，抽象事实为状态集合，汇合点取并集。
- `move` 后原变量置 `moved` 失效，借用随所有权转移；对失效变量借用/关闭分别报 `useAfterMove`。
- `borrow` 对已关闭变量报 `useAfterClose`（即不得把已关闭安全通道传给回调），未获取报 `useUninitialized`。
- 关闭时仍有活跃借用报 `borrowAcrossClose`；借用未归还就移动报 `moveWhileBorrowed`；归还死句柄报 `invalidRestore`；重复关闭报 `doubleClose`。
- `deferClose` 注册到 region，正常 `exitRegion` 与异常边展开均按 region 内 LIFO、region 间内层到外层执行。

## 编译插件调用

```ts
import { GraphBuilder, analyze, enumerateSequences } from './src/index.ts';

const graph = new GraphBuilder()
  .addNode('entry', { op: 'entry' })
  .addNode('acq', { op: 'acquire', variable: 'ch' })
  // ... addNode / addEdge（异常边第 3 参数为 'exception'）
  .build();

const result = analyze(graph);
for (const { violation, steps } of result.violations) {
  // violation.kind/location/variable/handle/deferred；steps 为见证路径
}

// 需要时枚举小图全部执行序列做比对
const concrete = enumerateSequences(graph, 50);
```

## 测试

零依赖，使用 Node 内置 test runner（需要 Node >= 23.9 的 type stripping）：

```
node --experimental-strip-types --test test/*.test.ts
```

覆盖嵌套清理、部分初始化失败、异常重抛、循环不动点（借还平衡与借用越过关闭）、移动失效，以及 7 张图上抽象违规集合与全部具体执行序列的一致性交叉验证（无环图要求精确匹配，含环图要求健全覆盖）。
