# async-tree-store

一个用于异步目录树的 TypeScript 状态库：节点展开时懒加载子项，用**节点代际
（generation）**处理"折叠 / 删除 / 重新展开发生在响应返回之前"的竞态。

## 并发契约

- **代际丢弃旧响应**：每次加载都打上节点当前代际的标签。`collapse` 会使代际
  +1，因此响应若来自折叠之前的代际，到达时直接丢弃；删除节点后，迟到的响应
  找不到节点，同样丢弃。
- **同代际共享一个请求**：同一节点同一代际内最多一个在途请求，重复 `expand`
  共享它，loader 只被调用一次。
- **失败保留已显示树**：加载失败进入 `error` 状态，但已展示的子节点原样保留；
  `retry(id)` 在同一代际上发起新请求。
- **删除无幽灵节点**：`remove(id)` 同步移除整个子树（含选择状态回退到父节点），
  任何迟到的响应都不会让已删节点复活。

## API 概览

```ts
import { TreeStore } from "./src/index.ts";

const store = new TreeStore({
  loader: async (id) => fetchChildren(id), // => Promise<{ id: string }[]>
});

store.expand("root");    // 展开并在需要时发起加载
store.collapse("root");  // 折叠并使在途响应失效（代际 +1）
store.refresh("root");   // 重新加载，期间保留已显示子项
store.retry("root");     // 仅在 error 状态下重新请求
store.remove("a");       // 删除子树；选中项回退到父节点
store.select("a");
store.getSelectionPath(); // ["root", "a"]
store.loads;              // loader 累计调用次数（加载计数）
```

## 运行测试

需要 Node ≥ 22.6（依赖原生 TypeScript 类型擦除）：

```sh
npm test
```

测试用可控 promise（`Deferred` + `MockLoader`）精确枚举响应到达顺序，覆盖：
同代际请求去重、折叠后重新展开的两种响应顺序、多代际乱序到达、失败重试保留
已显示树、删除后无幽灵节点、选择路径回退、加载计数核对。
