import { describe, it } from "node:test";
import assert from "node:assert/strict";

import {
  TouchGate,
  gateTouchSequence,
  FLAG_FULLY_OCCLUDED,
  FLAG_PARTIALLY_OCCLUDED,
  OCCLUSION_MASK,
  type TouchEvent,
  type Rect,
  type GatedClick,
} from "../src/index.ts";

const BOUNDS: Rect = { x: 0, y: 0, width: 100, height: 50 };

function event(
  action: TouchEvent["action"],
  pointerId: number,
  timestamp: number,
  flags = 0,
  x = 50,
  y = 25,
): TouchEvent {
  return { action, pointerId, x, y, timestamp, flags };
}

function gate(sink?: (click: GatedClick) => void): TouchGate {
  return new TouchGate({ bounds: BOUNDS, sink });
}

describe("全程遮挡", () => {
  it("down 带完全遮挡位时，即使 up 干净也不派发", () => {
    const clicks = gate().processSequence([
      event("down", 1, 0, FLAG_FULLY_OCCLUDED),
      event("up", 1, 10),
    ]);
    assert.deepEqual(clicks, []);
  });

  it("down 带部分遮挡位时同样作废", () => {
    const clicks = gate().processSequence([
      event("down", 1, 0, FLAG_PARTIALLY_OCCLUDED),
      event("up", 1, 10),
    ]);
    assert.deepEqual(clicks, []);
  });

  it("遮挡位组合出现也判定为遮挡", () => {
    assert.equal(OCCLUSION_MASK, 0x3);
    const clicks = gate().processSequence([
      event("down", 1, 0, 0x3),
      event("up", 1, 10),
    ]);
    assert.deepEqual(clicks, []);
  });

  it("作废手势结束后，全新的干净手势可恢复点击", () => {
    const gateInstance = gate();
    assert.deepEqual(
      gateInstance.processSequence([
        event("down", 1, 0, FLAG_FULLY_OCCLUDED),
        event("up", 1, 10),
      ]),
      [],
    );
    const clicks = gateInstance.processSequence([
      event("down", 2, 600),
      event("up", 2, 610),
    ]);
    assert.equal(clicks.length, 1);
    assert.equal(clicks[0].pointerId, 2);
  });
});

describe("抬手前撤走遮挡", () => {
  it("move 中途出现遮挡，撤走后 up 仍不派发", () => {
    const clicks = gate().processSequence([
      event("down", 1, 0),
      event("move", 1, 50, FLAG_PARTIALLY_OCCLUDED),
      event("move", 1, 80),
      event("up", 1, 100),
    ]);
    assert.deepEqual(clicks, []);
  });

  it("遮挡事件会启动冷却，冷却后的新手势才放行", () => {
    const gateInstance = gate();
    assert.deepEqual(
      gateInstance.processSequence([
        event("down", 1, 0),
        event("move", 1, 50, FLAG_FULLY_OCCLUDED),
        event("up", 1, 60),
      ]),
      [],
    );
    assert.equal(gateInstance.isCoolingDown(300), true);
    assert.equal(gateInstance.isCoolingDown(550), false);

    const clicks = gateInstance.processSequence([
      event("down", 2, 600),
      event("up", 2, 620),
    ]);
    assert.equal(clicks.length, 1);
  });
});

describe("冷却内点击", () => {
  it("up 距最近遮挡事件不足 500ms 时拦截", () => {
    const gateInstance = gate();
    gateInstance.processEvent(event("move", 9, 100, FLAG_PARTIALLY_OCCLUDED));

    const clicks = gateInstance.processSequence([
      event("down", 1, 200),
      event("up", 1, 300),
    ]);
    assert.deepEqual(clicks, []);
  });

  it("恰好 500ms 边界满足冷却要求（闭区间）", () => {
    const gateInstance = gate();
    gateInstance.processEvent(event("move", 9, 100, FLAG_PARTIALLY_OCCLUDED));

    const clicks = gateInstance.processSequence([
      event("down", 1, 550),
      event("up", 1, 600),
    ]);
    assert.equal(clicks.length, 1);
    assert.equal(clicks[0].timestamp, 600);
  });

  it("超过 500ms 后正常派发", () => {
    const gateInstance = gate();
    gateInstance.processEvent(event("move", 9, 100, FLAG_PARTIALLY_OCCLUDED));

    const clicks = gateInstance.processSequence([
      event("down", 1, 700),
      event("up", 1, 800),
    ]);
    assert.equal(clicks.length, 1);
  });

  it("up 自身带遮挡位时拦截并刷新冷却", () => {
    const gateInstance = gate();
    const blocked = gateInstance.processSequence([
      event("down", 1, 0),
      event("up", 1, 100, FLAG_PARTIALLY_OCCLUDED),
    ]);
    assert.deepEqual(blocked, []);
    assert.equal(gateInstance.isCoolingDown(400), true);
  });
});

describe("cancel 语义", () => {
  it("cancel 清除手势状态，之后立即开始的干净手势可点击", () => {
    const gateInstance = gate();
    const first = gateInstance.processSequence([
      event("down", 1, 0),
      event("move", 1, 20, 0, 50, 100),
      event("cancel", 1, 30),
    ]);
    assert.deepEqual(first, []);

    const clicks = gateInstance.processSequence([
      event("down", 2, 40),
      event("up", 2, 50),
    ]);
    assert.equal(clicks.length, 1);
    assert.equal(clicks[0].pointerId, 2);
  });

  it("cancel 不清除遮挡冷却，冷却内的新手势仍被拦截", () => {
    const gateInstance = gate();
    gateInstance.processSequence([
      event("down", 1, 0),
      event("move", 1, 50, FLAG_FULLY_OCCLUDED),
      event("cancel", 1, 60),
    ]);

    const blocked = gateInstance.processSequence([
      event("down", 2, 100),
      event("up", 2, 120),
    ]);
    assert.deepEqual(blocked, []);

    const allowed = gateInstance.processSequence([
      event("down", 3, 600),
      event("up", 3, 620),
    ]);
    assert.equal(allowed.length, 1);
  });
});

describe("双指同时触摸", () => {
  it("两个指针都干净且落在控件内时分别派发点击", () => {
    const clicks = gate().processSequence([
      event("down", 1, 0),
      event("down", 2, 10),
      event("up", 1, 20),
      event("up", 2, 30),
    ]);
    assert.equal(clicks.length, 2);
    assert.deepEqual(
      clicks.map((click) => click.pointerId),
      [1, 2],
    );
  });

  it("任一指针带遮挡位则整个手势作废，两个指针都不派发", () => {
    const clicks = gate().processSequence([
      event("down", 1, 0),
      event("down", 2, 10),
      event("move", 2, 15, FLAG_PARTIALLY_OCCLUDED),
      event("up", 1, 20),
      event("up", 2, 30),
    ]);
    assert.deepEqual(clicks, []);
  });

  it("指针按 ID 分别判定边界，一个在控件外不影响另一个点击", () => {
    const clicks = gate().processSequence([
      event("down", 1, 0, 0, 50, 25),
      event("down", 2, 10, 0, 999, 999),
      event("up", 1, 20, 0, 50, 25),
      event("up", 2, 30, 0, 999, 999),
    ]);
    assert.equal(clicks.length, 1);
    assert.equal(clicks[0].pointerId, 1);
  });

  it("先抬起的指针点击后，剩余指针被遮挡仍会被拦截", () => {
    const gateInstance = gate();
    const first = gateInstance.processSequence([
      event("down", 1, 0),
      event("down", 2, 10),
      event("up", 1, 20),
    ]);
    assert.equal(first.length, 1);

    const second = gateInstance.processSequence([
      event("move", 2, 25, FLAG_FULLY_OCCLUDED),
      event("up", 2, 30),
    ]);
    assert.deepEqual(second, []);
  });
});

describe("边界与异常输入", () => {
  it("down 在控件内、move 滑出后 up 回到控件内，不派发", () => {
    const clicks = gate().processSequence([
      event("down", 1, 0, 0, 50, 25),
      event("move", 1, 10, 0, 500, 500),
      event("up", 1, 20, 0, 50, 25),
    ]);
    assert.deepEqual(clicks, []);
  });

  it("down 在控件外、up 在控件内，不派发", () => {
    const clicks = gate().processSequence([
      event("down", 1, 0, 0, -1, -1),
      event("up", 1, 10, 0, 50, 25),
    ]);
    assert.deepEqual(clicks, []);
  });

  it("控件边界为闭区间，压在边上算在控件内", () => {
    const clicks = gate().processSequence([
      event("down", 1, 0, 0, 100, 50),
      event("up", 1, 10, 0, 0, 0),
    ]);
    assert.equal(clicks.length, 1);
  });

  it("未知 flags 位不视为遮挡", () => {
    const clicks = gate().processSequence([
      event("down", 1, 0, 0x4),
      event("up", 1, 10, 0x8),
    ]);
    assert.equal(clicks.length, 1);
  });

  it("没有对应 down 的 up 事件被忽略且不抛错", () => {
    const clicks = gate().processSequence([event("up", 7, 0)]);
    assert.deepEqual(clicks, []);
  });

  it("sink 与返回值收到同样的点击，processEvent 非点击返回 null", () => {
    const received: GatedClick[] = [];
    const gateInstance = gate((click) => received.push(click));
    assert.equal(gateInstance.processEvent(event("down", 1, 0)), null);
    const click = gateInstance.processEvent(event("up", 1, 10));
    assert.ok(click);
    assert.equal(received.length, 1);
    assert.deepEqual(received[0], click);
  });

  it("setBounds 后按新边界判定", () => {
    const gateInstance = gate();
    gateInstance.setBounds({ x: 200, y: 200, width: 10, height: 10 });
    const clicks = gateInstance.processSequence([
      event("down", 1, 0, 0, 50, 25),
      event("up", 1, 10, 0, 50, 25),
    ]);
    assert.deepEqual(clicks, []);
  });

  it("gateTouchSequence 一次性入口与类结果一致", () => {
    const sequence = [
      event("down", 1, 0),
      event("move", 1, 5, FLAG_FULLY_OCCLUDED),
      event("up", 1, 10),
      event("down", 2, 600),
      event("up", 2, 610),
    ];
    const clicks = gateTouchSequence(sequence, { bounds: BOUNDS });
    assert.equal(clicks.length, 1);
    assert.equal(clicks[0].pointerId, 2);
  });
});
