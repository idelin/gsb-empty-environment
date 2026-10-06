# focus-manager

用虚拟节点树管理弹层栈焦点的 TypeScript 实现，零依赖。

## 模型

- 每个弹层（layer）拥有一棵虚拟节点树，节点可禁用、可删除。
- 弹层构成栈；`openLayer` 记录触发节点及其祖先链（开层瞬间快照）。
- 关层焦点恢复顺序：**触发节点 → 最近仍可聚焦祖先 → 父层首项**。
- 恢复目标在恢复时刻才解析，关层期间发生的删除/禁用会被尊重。
- `closeLayer(id, { defer: true })` 异步恢复：每次栈变更递增 epoch，
  延迟恢复到点时若 epoch 已变（期间有开/关层），恢复作废——旧层不抢新层焦点。
- 弹层内容晚于开层渲染：栈顶层注册首个可聚焦节点时自动接管焦点。

## 运行测试

需要 Node ≥ 22.6（原生 type stripping，`.nvmrc` 锁定 v23.9.0）：

```sh
nvm use
npm test
```

## 事件重放

`test/replay.ts` 把声明式事件序列（`node`/`open`/`close`/`remove`/`disable`/
`flush`/`expectFocus`…）逐条重放到管理器上，异步恢复经手动队列由 `flush`
精确推进。`test/focus-manager.test.ts` 覆盖：禁用节点、跨层删除、重入打开、
异步关闭不抢焦点、深层栈逐级返回。
