# template-edit-kernel

TypeScript 实现的模板编辑内核：嵌套占位、镜像引用、纯函数变换，原子编辑与撤销。
零依赖，仅需 Node.js ≥ 23.6（原生 TypeScript 类型擦除 + 内置 `node:test`）。

## 能力

- **嵌套占位**：占位可包含子占位；父编辑覆盖子占位后子占位失效，撤销恢复其原身份（id / 区间 / 内容）。
- **镜像引用**：镜像占位跟随源占位同步更新；一个主占位可挂任意多个镜像。
- **纯函数变换**：镜像可挂 `(...texts) => string` 纯函数（支持多源，如菱形汇合）；注册即同步一次。
- **有向依赖编译**：`compile()` 构建拓扑序并拒绝循环引用，报出具体环链（如 `A -> B -> A`）；`edit` 前自动编译。
- **增量更新**：一次编辑只沿依赖边重算下游镜像，拓扑序保证菱形共享节点只算一次。
- **原子性**：`edit` 一次提交文本 + 占位树 + 依赖 + 撤销状态；变换中途抛错整体回滚。
- **重入隔离**：变换执行期间调用 `edit`/`addMirror` 抛 `ReentrancyError`，并触发整体回滚。
- **UTF-16 偏移**：公开偏移为 UTF-16 码元下标；注册范围切开代理对时抛 `InvalidRangeError`。
- **退出占位模式**：`finalize()` 丢弃全部占位状态，保留最终纯文本。

## 运行

```sh
node --test test/kernel.test.ts   # 测试
node examples/demo.ts             # 样例
```

## API 摘要

```ts
const k = new TemplateKernel(text);
k.addPlaceholder('name', start, end, parentId?);       // 主占位（可嵌套）
k.addMirror('shout', start, end, 'name', (s) => s.toUpperCase());
k.addMirror('join', start, end, ['B', 'C'], (b, c) => b + c); // 多源
k.compile();        // 静态检查：环 / 缺失源在此拒绝
k.edit('name', 'xy'); // 原子编辑 + 增量传播
k.undo();           // 撤销（恢复文本/占位树/依赖）
k.finalize();       // 退出占位模式，返回最终纯文本
```

## 运行样例输出

```
initial:       Hello ?. Shout: ?. Bye ?.
edit -> xy:    Hello xy. Shout: XY. Bye xy.
shout range:   { start: 17, end: 19 }
again text:    xy
undo:          Hello ?. Shout: ?. Bye ?.
finalize:      Hello Ada. Shout: ADA. Bye Ada.
covered:       greeting: ... (inner valid = false)
restored:      greeting: [hello world] (inner = "hello world")
cycle:         Circular placeholder reference detected: A -> B -> A
rollback:      "ok / ok" after Transform "anonymous" for placeholder "M" failed: transform exploded
```

## 测试结果（node --test，15/15 通过）

```
✔ 主占位改为 xy：两个镜像同时更新且偏移正确
✔ 镜像变换长度不同：后续占位不错位
✔ 菱形依赖去重：共享节点每次编辑只重算一次
✔ 增量更新：只重算被编辑占位的下游
✔ 循环引用在编译期拒绝并报出具体环链
✔ 自引用与长环
✔ 变换中途抛错：文本、占位树、依赖、撤销状态全部回滚
✔ 注册同步阶段的重入：抛 ReentrancyError 且注册无副作用
✔ 重入隔离（编辑传播阶段）
✔ 嵌套占位被父编辑覆盖后失效，撤销恢复原身份
✔ 镜像源被父编辑覆盖：镜像级联失效，撤销恢复
✔ UTF-16 偏移：禁止切开代理对
✔ LF 与 CRLF 两种换行：镜像同步与偏移一致正确
✔ 退出占位模式保留最终纯文本
✔ 撤销链：多次编辑逐步回退
ℹ pass 15 / fail 0
```
