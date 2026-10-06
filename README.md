# scroll-chain-model

TypeScript 滚动链模型：位移从最内层沿祖先链逐层消费，超界余量才向上传播。

## 模型

- `ScrollLayer`：单层状态，含 `position`、动态边界 `[min, max]` 与 `blocksChain`。
- `applyScrollDelta(layers, delta)`：从内层到外层依次消费位移。
  - 每层将 `position + delta` 钳制到自身边界内，只消费界内部分。
  - 超界余量传给父层；`blocksChain` 为真的层会吞掉余量，终止传播。
  - 零范围层（`min === max`）消费恒为零，但仍可阻断链式传播。
  - 守恒不变量：每层 `input === consumed + remainder`；全链
    `totalConsumed + overflow + discarded === delta`。
- `applyScale` / `applyScaleThenScroll`：缩放变化先保持锚点
  （锚点视口偏移对应的内容坐标不变），重算边界并钳制，之后再处理位移。

## 运行测试

需要 Node >= 22.6（依赖原生类型擦除直接运行 `.ts`）：

```sh
npm test
```

测试覆盖：正负位移、分数像素（含非二进分数）、动态边界伸缩、
零范围层阻断/穿透、缩放锚点保持与缩放-位移顺序，以及 500 组
随机场景的逐层守恒检查。
