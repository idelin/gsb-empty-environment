# fine-grained-patch-engine

基于响应式依赖的极简 UI 更新引擎：Signal 变更直接定位到具体的虚拟文本节点或属性绑定，
生成最小精准补丁（Patch），**无需全树 Diff** 即可完成界面局部对齐。

## 核心思路

1. **模板即数据**：用 `h(tag, props, ...children)` 描述界面；动态部分写成零参函数绑定，
   例如 `() => \`欢迎，${user.name}\``。
2. **挂载时建立依赖**：`mount()` 只遍历模板一次。每个文本/属性绑定被包装成独立的
   `Computation`，运行期间读到的每个 `Signal` 自动登记为它的依赖（见 `src/signal.ts`）。
3. **变更直达目标**：Signal 写入时只通知订阅了它的 Computation。Computation 重新求值后与
   旧值做标量比较，不一致则产出一条精准补丁（见 `src/engine.ts`）：

   | Patch | 含义 |
   | --- | --- |
   | `UpdateTextNode` | 更新指定路径的虚拟文本节点 |
   | `SetAttribute` / `RemoveAttribute` | 设置/移除指定元素的属性 |
   | `InsertNode` / `RemoveNode` | 在 `when()` 锚点处插入/移除子树 |

   补丁的 `target` 是挂载期分配的稳定路径（如 `0/2/0`），`applyPatch()` 按路径直达节点，
   全程没有组件重渲染、没有树对比。

## 验收边界

节点包含 `'欢迎，' + user.name`；当 `user.name` 改变时，引擎跳过整个组件树的对比，
直接产出 `UpdateTextNode` 指令精准作用于目标节点：

```ts
const user = reactive({ name: "世界" });
const engine = mount(h("div", null, h("p", null, () => `欢迎，${user.name}`)));

user.name = "李四";
engine.drainPatches();
// => [ { op: "UpdateTextNode", target: "0/0/0", value: "欢迎，李四" } ]
```

对应测试：`tests/engine.test.ts` 中的
`验收边界: user.name 变化直接产出 UpdateTextNode 精准补丁，无全树 Diff`
（同时断言组件函数执行次数恒为 1，证明未发生全树重渲染/Diff）。

## 环境要求

- Node.js >= 22.6（使用 `--experimental-strip-types` 直接运行 TypeScript；
  Node >= 23.6 可省略该标志）。仓库内 `.nvmrc` 指定 22.14.0。
- 无第三方运行时依赖。

## 运行

```bash
npm test        # Node.js 内置测试运行器（node:test + assert）
npm run demo    # 运行 examples/demo.ts，打印初始渲染、每次变更生成的补丁与更新后界面
```

可选类型检查（需联网安装 devDependencies）：`npm install -D typescript @types/node && npm run typecheck`。

## 目录结构

- `src/signal.ts` — Signal / Computation / 依赖追踪 / `batch` / `reactive`（属性级响应式）
- `src/vnode.ts` — 虚拟节点类型与模板 DSL（`h`、`when`）
- `src/patch.ts` — Patch 类型、`applyPatch`、路径寻址、`toHTML`
- `src/engine.ts` — `mount()`：绑定订阅、补丁生成、界面局部对齐
- `tests/engine.test.ts` — 10 个 `node:test` 用例
- `examples/demo.ts` — 可运行样例
