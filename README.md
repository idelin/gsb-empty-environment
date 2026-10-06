# layered-focus-manager

TypeScript 焦点管理器：用**虚拟节点树**管理**弹层栈**的焦点。

## 核心规则

1. **关闭子层回到触发节点**：每层记录打开它的 `triggerId`，关闭后焦点回到该节点。
2. **触发节点失效逐级回退**：触发节点已删除 / 禁用 / 不可聚焦时，沿虚拟树向上找
   最近仍可聚焦的祖先；祖先也不可用时，退到父层（再依次往下）的第一个可聚焦项（DFS）。
3. **异步关闭不抢焦点**：`closeLayer` 等待 `transition`（如下滑动画）结束后才移除层并
   恢复焦点。期间任何更新的打开 / 关闭 / 聚焦动作都会递增 `restoreToken` 纪元，
   使过期的恢复操作失效——旧层的异步关闭永远不会抢走新层的焦点；仅当焦点滞留
   在已关闭层内时才做兜底转移。
4. **重入安全**：事件回调中同步打开新层（重入）同样通过纪元机制保证最终状态一致。
5. **级联关闭**：关闭某层会连带关闭它当前的所有子层（子层事件先于父层发出）。

## API

```ts
const m = new FocusManager({ transition: () => animateClose() });
m.openLayer({ id: 'menu', triggerId: 'btn2', root: { id: 'root', focusable: false, children: [...] } });
await m.closeLayer('menu');   // 异步：过渡结束后恢复焦点
m.removeNode('btn2');         // 删除节点（焦点在其中时自动转移）
m.setDisabled('btn2', true);  // 禁用节点（同上）
m.focus('mi1');               // 手动聚焦，不可聚焦时返回 false
m.onEvent((e) => { ... });    // 订阅事件；m.log 为完整事件日志
```

节点：`focusable: false` 表示静态不可聚焦（容器），`disabled` 表示动态禁用，
`removeNode` 标记删除；三者都不会成为焦点目标，删除/禁用会保留 `parentId`
供祖先回退查找。

事件：`layer:opened` / `layer:closing` / `layer:closed` / `focus:change`（含
`reason`：`layer-open` / `restore` / `api` / `node-removed` / `node-disabled` /
`layer-close`）/ `node:removed` / `node:disabled`。

## 事件重放测试

`test/replay.test.mjs` 把场景脚本化（open / close / remove / disable / focus /
flush），断言完整事件流与黄金日志逐条一致，并将同一脚本**重放两次**验证确定性。
覆盖：禁用节点、跨层删除（触发节点在父层被删）、重入打开（closing/closed 回调中
开新层）、异步关闭不抢新层焦点、级联关闭。

```sh
npm test     # 运行全部事件重放测试
npm run build  # 生成 dist/focus-manager.mjs（擦除类型后的可运行产物）
```

## 环境说明

本仓库开发环境无网络、无 tsc。`src/focus-manager.ts` 只使用**可擦除**的 TS 语法
（类型注解 / interface / type 别名，不用 enum、参数属性、非空断言等），
`scripts/strip-types.mjs` 是零依赖的类型擦除器，测试通过它直接运行 TS 源码，
类型纯擦除、不改变运行语义。有网络时可 `npm i -D typescript && npx tsc --noEmit`
做完整类型检查（`tsconfig.json` 已备好）。
