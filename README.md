# 多域双向互模拟代理视图

面向 TypeScript 插件宿主的可导入组件：在三个及以上隔离域之间往返传递只读代理对象时，用循环策略图上的严格互模拟保持身份一致；用 grant lease 做细粒度撤权，且不会因拆封、重包装或 `enumerate` 顺序而放宽能力或泄露已撤销属性。

> 输入是不可变描述图，严禁执行图中的函数值。`invoke` / `construct` 只读取符号化调用描述。

## 快速开始

```ts
import { createHost } from "./src/index.ts";

const graph = {
  root: "root",
  nodes: {
    root: {
      kind: "object",
      prototype: "base",
      properties: {
        name: { value: "root" },
        child: { ref: "child" },
        fn: { ref: "fn" },
      },
    },
    child: { kind: "object", prototype: "base", properties: { n: { value: 1 } } },
    base: { properties: { inherited: { value: "base-value" } } },
    fn: {
      kind: "function",
      invoke: { ref: "child" },
      construct: { ref: "child" },
    },
  },
};

const policy = {
  entry: "a",
  nodes: {
    a: { domain: "A" },
    b: { domain: "B" },
    c: { domain: "C" },
    a2: { domain: "A" },
  },
  edges: [
    { id: "ab", from: "a", to: "b", action: "pass" },
    { id: "bc", from: "b", to: "c", action: "pass" },
    { id: "ca", from: "c", to: "a2", action: "pass" },
    { id: "a2b", from: "a2", to: "b", action: "pass" },
  ],
};

const host = createHost({ graph, policy });

const inA = host.view("root");
const inB = host.pass(inA, "ab");
const inC = host.pass(inB, "bc");
const backInA = host.pass(inC, "ca");

backInA === inA;                    // true: 同一身份映射被恢复
host.isSameIdentity(backInA, inA); // true
host.domainOf(backInA);             // "A"

host.view("child") === inA.child;   // true: 嵌套视图也共享缓存身份
inA.child.inherited;                // "base-value": 原型链查找
typeof host.view("fn");             // "function"
host.view("fn")();                  // 符号化 invoke 结果
```

## 描述图

`DescriptionGraph` 接受 `{ root?, nodes }`，`nodes` 可以是数组或 `id -> NodeSpec` 记录。每个 `NodeSpec` 支持：

- `kind`: `"object" | "array" | "function"`。
- `prototype` / `proto`: 原型链引用，可写成节点 id，或 `{ ref, capabilities, requires, grants }`。
- `properties` / `props`: 自有属性。属性值可以是原始值、`{ $ref: "id" }` 或 `{ value, writable, enumerable, configurable, capabilities, requires, grants }`。
- `invoke` / `call`: 符号化调用描述 `{ result, ref, throws, capabilities }`。
- `construct` / `new`: 符号化构造描述。构造结果必须是对象节点引用。

`kind: "array"` 只用于描述数组形态；代理 target 使用空对象以保证撤权时 `ownKeys` 不变量始终成立，因此不要依赖 `Array.isArray(proxy)`。

图在装载时会做防御性规范化：

- 拒绝真实函数值、访问器（getter/setter）和 symbol 键。
- 不调用任何携带函数；读到访问器描述时直接抛 `UnsafeDescriptionError`。
- 拒绝未知节点引用、重复 id、非法 capability。
- 内部只保留规范化后的不可变数据，不把原始 spec 暴露给代理。

## 能力衰减

能力为 `read`、`write`、`invoke`、`construct`、`enumerate` 的有限集合。所有组合操作都是交集：

- `view(..., { capabilities })`
- policy 节点的 `capabilities`
- policy 边的 `capabilities` / `attenuate` / `deny`
- 属性的 `capabilities` / `requires`
- `host.attenuate()` 与 `host.rewrap()`

不存在把能力重新并回路径。`rewrap(view, { capabilities: ALL })` 只会保留当前能力；把高能力视图写进属性时，读取端最多得到写入时允许的能力。`enumerate` 独立于 `read`：有 `enumerate` 无 `read` 时可见键名但不能取值。

`host.attenuate(view, caps)` 的含义是“把视图上界设为 `caps`（与当前能力取交集）”，不是“从当前能力里删除 `caps`”；policy 边的 `attenuate` 字段则按名字表示删除。

`write` 写入的是 host 内部的 overlay：输入描述图本身永不修改；overlay 属于同一底层对象，因此其他域在获得 `read` 时能看到写入结果，但仍受各自能力与授权边约束。

## 身份与互模拟

身份记录键由三部分组成：

1. 底层描述节点 id；
2. 当前有效 capability mask；
3. 当前策略配置的严格互模拟类。

策略配置是 `(policy node, capability mask)`。互模拟在有限标记迁移系统上做分区细化（greatest fixed point）：

- observation 包含域与 capability；
- 迁移标签包含边种类、action 和目标域；
- 环、折叠节点和不同 id 但同构的策略片段会收敛到同一个类；
- 不比较代理对象地址，也不比较路径字符串。

`host.isSameIdentity(a, b)` 使用上述类判定；`host.identityOf(view)` 返回身份记录 token。
折叠后的等价策略节点可能复用同一个代表节点，因此 `policyNodeOf(view)` 返回的是当前 lease 的代表 id，而不是调用方最后经过的字面节点 id。
对折叠节点使用另一条等价路径的 `edgeId` 时，组件会解析到当前代表的严格等价边；需要路径级精确撤权时应使用当前代表上的显式 edge id。

### 身份恢复与 lease

为了让“同一底层对象经独立授权分支”能够被单独撤销，代理还带一个 lease：

- 正常创建的视图 lease 由 grant 集合决定；
- 沿路径往返时，组件会保留路径锚点；再次到达与某个锚点相同底层节点、相同能力、互模拟等价策略配置时，直接复用该锚点的代理。因此 `A -> B -> C -> A`（即使中间边使用不同 id 或属于 grant）恢复原身份；
- 两条独立菱形分支得到相同 `identityOf` / `isSameIdentity === true`，但代理 `!==`，这样一条分支撤销不会误伤另一条。

如果往返路径衰减了能力，则能力 mask 不同，身份不会合并；这正是防止“重包装提权”的边界。

## 撤权

policy 边默认是 `grant`（可撤销授权）。只做路由、不参与既有视图撤销依赖的边可写 `{ kind: "transfer" }`。`revoke(edgeId)` 是同步的：

- 所有 grant `support` 包含该边的视图立即失效，或按 `onRevoke` 降级；
- 独立分支的视图（即使共享 bisimulation identity）不受影响；
- 依赖该边的 pending 异步结果立即以 `RevokedViewError` reject；
- `onRevoke` 监听器只收到自己声明依赖的边的事件。

```ts
const event = host.revoke("eL2");
event.invalidated; // 受影响的视图快照
event.downgraded;  // 被降级但未失效的视图快照
```

边可以声明降级而不是失效：

```ts
{
  id: "eL2",
  from: "l",
  to: "t",
  onRevoke: { attenuate: "read" },
}
```

此时视图保留其余能力，但不能再读取；立即读取会抛 `CapabilityError`。pending 结果仍然 reject，因为它们是在旧授权下计算的。

### 属性级撤权与 enumerate

属性可声明：

```ts
secret: { value: "...", grants: "grantEdge" }
```

`grants` 为数组时表示所有列出的授权边都必须有效（AND）。只要其中一条被撤销，该属性在 `get`、`in`、`getOwnPropertyDescriptor`、`Reflect.ownKeys` 和 `Object.keys` 中都不存在。`ownKeys` 先过滤不可见属性，再按“数组索引数值序、其余键 UTF-16 码元序”的稳定规范顺序返回，因此不会通过“少了一个键”造成顺序槽位泄露，也不会把已撤销键排在可观察位置。

## 反射与不执行

- `get` 返回原始值或新的代理视图，绝不返回真实底层对象。
- 对象代理的 target 是空对象；函数代理的 target 是绑定的占位函数，`apply` / `construct` 全部被 trap 拦截。
- `then`、`Symbol.toPrimitive`、`Symbol.iterator`、`constructor`、`__proto__` 等反射/协议入口不会泄露 target。
- `invoke` / `construct` 只读取 `CallSpec`；`throws` 直接转成 `SymbolicOperationError`。
- 原始对象和真实函数不能被写入或重新传入；透明转发品牌 token 的外部包装会复用原 lease，无法透明转发则 fail closed；能力仍只会被交集。

## 主要 API

```ts
createHost(options) / createBisimulationHost(options)

host.view(nodeId?, { domain?, policyNode?, capabilities? })
host.viewIn(domain, nodeId?, options?)
host.pass(view, edgeIdOrDomain, domainOrEdge?)
host.attenuate(view, capabilities)
host.rewrap(view, { capabilities?, domain?, policyNode?, edgeId? })
host.invoke(view, args?) / host.construct(view, args?)

host.isSameIdentity(a, b)
host.identityOf(view)
host.capabilitiesOf(view)
host.domainOf(view) / host.policyNodeOf(view) / host.nodeIdOf(view)
host.isRevoked(view) / host.isView(value)

host.revoke(edgeId, options?)
host.restore(edgeId)
host.onRevoke(listener, dependencies?)

host.requestView(...) / host.asyncView(...)
host.requestPass(...) / host.asyncPass(...)
host.requestInvoke(...) / host.asyncInvoke(...)

host.snapshot()
host.policyModel()
```

`snapshot()` 不含对象地址，只包含符号节点 id、能力、grant/trace、身份 token、策略类计数和待决请求数，适合做结构性断言。

异步请求可传 `{ dependencies: [edgeId] }`，把结果额外绑定到指定授权边；该边在 settle 前撤销时 promise 以 `RevokedViewError` reject。

## 有限状态关系模型

`FiniteStateModel` / `computeBisimulation` 是独立导出的有限状态图工具：

```ts
import { FiniteStateModel } from "./src/index.ts";

const model = new FiniteStateModel([
  { id: "A0", observation: "A", transitions: [{ label: "pass", to: "B0" }] },
  { id: "B0", observation: "B", transitions: [{ label: "pass", to: "A2" }] },
  { id: "A2", observation: "A", transitions: [{ label: "pass", to: "B0" }] },
]);

model.bisimulation().isBisimilar("A0", "A2"); // true
model.reachableFrom("A0");
model.hasPath("A0", "A2", "pass");
```

测试用它对照三域往返、菱形部分撤权和非反射不执行约束。

## 测试

```bash
npm test
```

测试覆盖：

- `test/roundtrip.test.ts`: 三域往返、折叠策略节点、原型链、嵌套身份、能力衰减与重包装。
- `test/diamond.test.ts`: 菱形双分支、单边撤权、降级、异步 reject、属性级撤权与 enumerate 顺序。
- `test/nonreflect.test.ts`: 函数/访问器拒绝、符号化 invoke/construct、反射隔离、原始值写入拒绝。
- `test/model.test.ts`: 循环有限状态互模拟、策略图模型、三域往返与菱形撤权的可达性对照。
- `test/capability.test.ts`: 属性/原型边/节点/边上的能力衰减与 `requires` 隐藏。
- `test/api.test.ts`: 数组图、根覆盖、record 形式策略边、快照与 restore 行为。
