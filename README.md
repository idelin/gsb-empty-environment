# 敏感控件触摸遮挡门禁（touch-gate）

支付与授权界面上的确认按钮可能被另一个应用的半透明窗口盖住：攻击者让用户以为在点别的控件，实际点中的是确认键，遮挡窗口还会在抬手前一刻撤走。本包是一个零依赖的 TypeScript 触摸门禁，嵌入控件事件通道后逐事件过滤触摸序列，只把可信点击派发给敏感控件。

## 门禁规则

- 任一事件（down/move/up/cancel）带遮挡位（`0x1` 完全遮挡、`0x2` 部分遮挡），整个手势立即作废，之后的事件一律不派发点击。
- 点击派发须同时满足：该指针的 down 与 up 都落在控件边界内（闭区间，压边算在内）、全程无遮挡位、且 up 距最近一次带遮挡位的事件至少 500 毫秒。
- `cancel` 清除当前手势的全部指针与作废状态，但**不**清除遮挡冷却计时。
- 多指按 `pointerId` 分别跟踪边界，任一指针触发遮挡则整个手势作废。
- 手势在最后一个指针 up/cancel 后结束；冷却时间戳跨手势保留。

## 安装与接入

本包以 TypeScript 源码直接交付（ESM），无第三方依赖，Node 22.6+ 可用 `--experimental-strip-types` 直接运行，框架侧也可经自有构建链打包。

```ts
import { TouchGate, type TouchEvent } from "./src/index.ts";

const gate = new TouchGate({
  bounds: { x: 0, y: 0, width: 120, height: 48 },
  sink: (click) => confirmButton.dispatchClick(click),
});

// 把平台原始触摸事件归一化为 TouchEvent 后喂入门禁
function onPlatformTouch(raw: TouchEvent): void {
  gate.processEvent(raw); // 只有通过门禁的点击才会进入 sink
}
```

也可以对一整条事件序列做一次性判定：

```ts
import { gateTouchSequence } from "./src/index.ts";

const clicks = gateTouchSequence(events, { bounds });
```

## API

- `new TouchGate(options)`：`options.bounds` 为控件边界，`options.cooldownMs` 默认 500，`options.sink` 为可选点击接收方。
- `processEvent(event): GatedClick | null`：处理单个事件，返回通过门禁的点击或 `null`。
- `processSequence(events): GatedClick[]`：依次处理序列并收集全部点击。
- `setBounds(bounds)`：布局变化时更新控件边界。
- `reset()`：清除手势状态（保留冷却计时，等同 cancel 语义）。
- `isCoolingDown(timestamp)`：查询给定时间戳是否仍在遮挡冷却内。

`TouchEvent` 字段：`action`（`down | move | up | cancel`）、`pointerId`、`x`、`y`、`timestamp`（毫秒）、`flags`（`0x1` / `0x2`，可用 `FLAG_FULLY_OCCLUDED`、`FLAG_PARTIALLY_OCCLUDED`、`OCCLUSION_MASK`）。

## 测试

```sh
npm test
```

24 个用例覆盖：全程遮挡、抬手前撤走遮挡、冷却内点击（含 500ms 边界）、cancel 后立刻点击（含冷却保留）、双指同时触摸，以及边界闭区间、滑出控件、未知 flags 位等异常输入。
