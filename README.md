# 细粒度 DOM / 虚拟节点更新补丁生成器

基于响应式依赖的极简 UI 更新引擎：Signal 变更直接定位到具体的虚拟文本节点或
属性绑定，生成最小精准补丁（Patch），**无需全树 Diff** 即可完成界面局部对齐。

## 核心机制

1. **声明绑定**：模板中动态部分用 `t(() => ...)`（文本）和 `attr(() => ...)`（属性）声明。
2. **依赖收集**：`mount` 时在响应式上下文中求值每个绑定，Signal 被读取即自动登记
   「Signal → 绑定 → 节点 id」的精确映射。
3. **精准更新**：Signal 变更只重算依赖它的绑定，对比新旧标量值（每个绑定持有
   `last` 影子值），产出最小补丁：
   - `UpdateTextNode { id, text }`
   - `SetAttribute { id, name, value }`
   - `RemoveAttribute { id, name }`
4. **局部对齐**：`applyPatches(index, patches)` 通过节点 id 寻址表直接作用目标节点。

全程不遍历、不对比组件树。测试中用 `stats.evaluations` 计数证明：一次 Signal
变更仅触发依赖绑定的重算，其余绑定零求值。

## 目录结构

```
src/signal.ts   响应式 Signal（依赖追踪 + 精准通知）
src/engine.ts   模板 DSL（h/t/attr）+ 挂载与补丁生成引擎
src/vnode.ts    虚拟节点、Patch 类型、applyPatches、renderToString
test/           Node.js 内置测试（node:test）
examples/demo.ts 运行样例
```

## 运行

需要 Node.js ≥ 22.6（使用 `--experimental-strip-types` 直接运行 TS，零依赖、零构建）：

```bash
npm test     # node --experimental-strip-types --test test/*.test.ts
npm run demo # node --experimental-strip-types examples/demo.ts
```

## 验收场景

节点包含 `'欢迎，' + user.name`；当 `user.name` 改变时，引擎跳过整个组件树的
对比，直接产出：

```json
{ "op": "UpdateTextNode", "id": 7, "text": "欢迎，Bob" }
```

精准作用于目标节点。对应测试：
`test/engine.test.ts` 中「验收场景：user.name 变更 → 仅产出一条 UpdateTextNode 精准补丁」。
