import { test } from "node:test";
import assert from "node:assert/strict";
import {
  TouchGate,
  FLAG_FULLY_OBSCURED,
  FLAG_PARTIALLY_OBSCURED,
  type TouchEvent,
  type DispatchedEventType,
} from "./index.ts";

const BOUNDS = { left: 100, top: 100, right: 200, bottom: 200 };

function ev(
  action: TouchEvent["action"],
  timestamp: number,
  overrides: Partial<TouchEvent> = {},
): TouchEvent {
  return {
    action,
    pointerId: 1,
    x: 150,
    y: 150,
    timestamp,
    flags: 0,
    ...overrides,
  };
}

function run(gate: TouchGate, events: TouchEvent[]): DispatchedEventType[] {
  return events.flatMap((e) => gate.handle(e)).map((d) => d.type);
}

test("正常手势：down/up 均在边界内且全程无遮挡，派发完整序列与点击", () => {
  const gate = new TouchGate(BOUNDS);
  const out = run(gate, [
    ev("down", 0),
    ev("move", 50, { x: 160 }),
    ev("up", 100, { x: 160 }),
  ]);
  assert.deepEqual(out, ["down", "move", "up", "click"]);
});

test("up 落在边界外不派发点击", () => {
  const gate = new TouchGate(BOUNDS);
  const out = run(gate, [ev("down", 0), ev("up", 100, { x: 500 })]);
  assert.deepEqual(out, ["down", "up"]);
});

test("down 落在边界外不派发点击", () => {
  const gate = new TouchGate(BOUNDS);
  const out = run(gate, [
    ev("down", 0, { x: 500 }),
    ev("move", 50, { x: 150 }),
    ev("up", 100),
  ]);
  assert.deepEqual(out, ["down", "move", "up"]);
});

test("全程遮挡：down 带遮挡位，整个手势不派发任何事件", () => {
  const gate = new TouchGate(BOUNDS);
  const out = run(gate, [
    ev("down", 0, { flags: FLAG_FULLY_OBSCURED }),
    ev("move", 100),
    ev("up", 200),
  ]);
  assert.deepEqual(out, []);
});

test("抬手前一刻撤走遮挡：中途 move 带遮挡位，之后事件一律不派发", () => {
  const gate = new TouchGate(BOUNDS);
  const out = run(gate, [
    ev("down", 0),
    ev("move", 300, { flags: FLAG_PARTIALLY_OBSCURED }),
    // 遮挡窗口撤走，后续事件不再带遮挡位
    ev("move", 480),
    ev("up", 490),
  ]);
  assert.deepEqual(out, ["down"]);
});

test("冷却内点击：up 距最近一次遮挡事件不足 500ms 不派发点击，满 500ms 后放行", () => {
  const gate = new TouchGate(BOUNDS);
  // 第一个手势带遮挡，作废
  assert.deepEqual(
    run(gate, [
      ev("down", 0, { flags: FLAG_FULLY_OBSCURED }),
      ev("up", 100),
    ]),
    [],
  );
  // 干净手势，但 up(400) 距遮挡事件(0) 不足 500ms
  assert.deepEqual(run(gate, [ev("down", 200), ev("up", 400)]), ["down", "up"]);
  // 冷却恰好满 500ms，放行点击
  assert.deepEqual(
    run(gate, [ev("down", 450), ev("up", 500)]),
    ["down", "up", "click"],
  );
});

test("cancel 清除手势状态但不清除遮挡冷却，cancel 后立刻点击被拦截", () => {
  const gate = new TouchGate(BOUNDS);
  // 遮挡手势被 cancel：cancel 本身因手势已作废而不派发
  assert.deepEqual(
    run(gate, [
      ev("down", 0, { flags: FLAG_FULLY_OBSCURED }),
      ev("cancel", 100),
    ]),
    [],
  );
  // 冷却仍在：up(300) 距遮挡事件(0) 不足 500ms，无点击
  assert.deepEqual(run(gate, [ev("down", 200), ev("up", 300)]), ["down", "up"]);
  // 冷却过后手势状态已干净，正常派发点击
  assert.deepEqual(
    run(gate, [ev("down", 600), ev("up", 700)]),
    ["down", "up", "click"],
  );
});

test("干净手势的 cancel 正常派发并清除手势状态", () => {
  const gate = new TouchGate(BOUNDS);
  assert.deepEqual(
    run(gate, [ev("down", 0), ev("cancel", 100)]),
    ["down", "cancel"],
  );
  // 手势已清除：游离的 up 不派发，新 down 重新开始
  assert.deepEqual(
    run(gate, [ev("up", 150), ev("down", 200), ev("up", 300)]),
    ["down", "up", "click"],
  );
});

test("双指同时触摸：任一指针被遮挡则整个手势作废", () => {
  const gate = new TouchGate(BOUNDS);
  const out = run(gate, [
    ev("down", 0, { pointerId: 1 }),
    ev("down", 50, { pointerId: 2, flags: FLAG_FULLY_OBSCURED }),
    ev("move", 100, { pointerId: 1 }),
    ev("up", 150, { pointerId: 1 }),
    ev("up", 160, { pointerId: 2 }),
  ]);
  assert.deepEqual(out, ["down"]);
  // 全部手指抬起后手势结束，冷却过后可正常点击
  assert.deepEqual(
    run(gate, [ev("down", 700), ev("up", 800)]),
    ["down", "up", "click"],
  );
});

test("多指各自判定：干净指针的 move/up 在遮挡指针抬起前仍受作废约束", () => {
  const gate = new TouchGate(BOUNDS);
  const out = run(gate, [
    ev("down", 0, { pointerId: 1 }),
    ev("down", 10, { pointerId: 2 }),
    ev("move", 20, { pointerId: 2, flags: FLAG_PARTIALLY_OBSCURED }),
    ev("up", 30, { pointerId: 2 }),
    // 指针 2 已抬起，但指针 1 仍在手势中，手势整体已作废
    ev("up", 40, { pointerId: 1 }),
  ]);
  assert.deepEqual(out, ["down", "down"]);
});
