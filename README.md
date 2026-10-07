# event-tree

沿虚拟树进行捕获 / 目标 / 冒泡三阶段分发的 TypeScript 事件库（零依赖，Node >= 22.6 直接运行 `.ts`）。

## 用法

```ts
import { EventTree } from "./src/event-tree.ts";

const tree = new EventTree();
const root = tree.createNode();
const child = tree.createNode(root);

tree.addEventListener(root, "hit", (e) => console.log("bubble", e.phase));
tree.addEventListener(root, "hit", (e) => console.log("capture", e.phase), { capture: true });
tree.addEventListener(child, "hit", (e) => console.log("target"), { once: true });

tree.dispatchEvent(child, { type: "hit", data: 1, bubbles: true });
```

## 语义约定

- **路径快照**：`dispatchEvent` 开始时计算 root → target 的路径；分发期间增删节点（`removeNode` / `setParent` / `createNode`）不改变本次路径。
- **阶段顺序**：capture（root→父）→ target（目标节点上 capture 与 bubble 监听按注册顺序触发）→ bubble（父→root）。`bubbles: false` 跳过冒泡。
- **once**：监听在**调用前**移除，因此回调内重入分发同一事件不会再次触发它。
- **stopPropagation**：当前节点剩余监听继续执行，后续节点不再访问。
- **stopImmediatePropagation**：当前节点剩余监听及所有后续传播立即终止。
- **监听移除立即生效**：分发中 `removeEventListener` 后，该监听即使在本次路径的后续节点（或同节点快照中）也不会被调用；新增监听对已开始迭代的节点本次不生效。
- **removeNode**：子节点重新挂到被删节点的父节点；被删节点保留其监听注册（与 DOM 摘除元素一致），因此进行中的分发仍会触发它。
- **重入分发**：回调内可安全地再次 `dispatchEvent`，内层事件完整传播完毕后外层继续。
- **回调抛错**：默认收集所有异常、继续传播，分发结束后抛出 `AggregateError`；传入 `onListenerError` 回调则改为逐个上报且不抛出。

## 测试

```sh
npm test   # node --test test/event-tree.test.ts
```
