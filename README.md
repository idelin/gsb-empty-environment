# chat-layout

聊天列表布局库：按需测量的不等高项目、前缀和定位、滚动锚定。TypeScript 编写，零依赖，直接由 Node.js（>= 22.6，原生类型擦除）运行。

## 核心能力

- **按需测量**：项目初始使用统一的 `estimatedHeight`，渲染后通过 `measure(id, height)` 写入真实高度，`unmeasure(id)` 还原估值。
- **按偏移定位**：`locate(offset)` 基于 Fenwick 树（`src/fenwick.ts`）在 O(log n) 内找到包含该偏移的项目及项目内偏移；`offsetOf(id)` 给出项目顶部偏移。
- **滚动锚定**：`update(ops, scrollOffset)` 在应用批量更新（头部插入 / 任意位置插入 / 删除 / 高度修正）后返回新的滚动偏移，保持首个可见项目及其项目内偏移不变。
- **锚点删除回退**：锚点项目被删除时，锚定到其原后继的顶部；无后继时锚定到原前驱的底部（高度取同批次修正后的值）。

## API 摘要

```ts
import { ChatLayout } from './src/index.ts';

const layout = new ChatLayout({ estimatedHeight: 50 });

// 批量更新 + 锚定：返回修正后的滚动偏移
const nextScroll = layout.update(
  [
    { type: 'prepend', items: [{ id: 'm1' }, { id: 'm2' }] },
    { type: 'measure', id: 'm7', height: 132 },
    { type: 'remove', ids: ['m9'] },
  ],
  scrollOffset,
);

// 低层接口
const anchor = layout.captureAnchor(scrollOffset); // { id, offsetIn } | null
const adjusted = layout.applyBatch(ops, anchor);   // number | undefined
layout.locate(offset);                             // { index, offsetIn }
layout.visibleRange(scrollOffset, viewportHeight); // { startIndex, endIndex }
layout.offsetOf(id);                               // 项目顶部偏移
layout.totalHeight;                                // 总高度
```

## 语义约定

- `locate(offset)` 将偏移钳制到 `[0, totalHeight]`；偏移恰好等于总高度时定位到最后一项的底部。
- 锚点项目自身在同批次内变矮时，项目内偏移钳制到新高度。
- 锚点无法解析（列表为空、所有项目被删）时，`update` 将滚动偏移钳制到 `[0, totalHeight]`。
- 高度必须为有限的非负数；重复的项目 id 会被拒绝。

## 测试

```
npm test
```

测试包含一个独立实现的全量前缀和参考模型（`test/ref-model.ts`），对连续随机批量更新（插入 / 删除 / 测量 / 还原）与边界滚动（0、总高度、越界、负值）做逐轮交叉核对。
