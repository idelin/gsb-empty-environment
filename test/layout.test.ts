import assert from "node:assert/strict";
import test from "node:test";
import { dialogTree, glyphMetrics } from "../src/fixtures.ts";
import { findBox, layout, measureText, nodeIds } from "../src/layout.ts";

test("text measurement uses fixture glyph advances", () => {
  // "editor" in ui@16: e9 d10 i4 t6 o10 r6 = 45
  const m = measureText("editor", "ui", 16, glyphMetrics);
  assert.equal(m.width, 45);
  assert.equal(m.height, 22);
  // 缩放：size 32 宽度翻倍
  assert.equal(measureText("editor", "ui", 32, glyphMetrics).width, 90);
  // 缺失字形走默认步进
  assert.equal(measureText("?", "mono", 16, glyphMetrics).width, 8);
});

test("nested layout computes deterministic geometry", () => {
  const box = layout(dialogTree, glyphMetrics);
  assert.equal(box.id, "dialog");
  assert.equal(box.width, 110); // 24 + max(45, 64, 86)
  assert.equal(box.height, 112); // 24 + 22 + 20 + 30 + 2*8

  const title = findBox(box, "title")!;
  assert.deepEqual([title.x, title.y, title.width, title.height], [12, 12, 45, 22]);

  const status = findBox(box, "status")!;
  assert.deepEqual([status.x, status.y], [12, 42]);

  const buttons = findBox(box, "buttons")!;
  assert.deepEqual([buttons.x, buttons.y, buttons.width, buttons.height], [12, 70, 86, 30]);

  const ok = findBox(box, "btn-ok")!;
  assert.deepEqual([ok.x, ok.y, ok.width], [16, 74, 20]);
  const cancel = findBox(box, "btn-cancel")!;
  assert.deepEqual([cancel.x, cancel.y, cancel.width], [44, 74, 50]);
});

test("layout is deterministic and origin-aware", () => {
  const a = layout(dialogTree, glyphMetrics);
  const b = layout(dialogTree, glyphMetrics);
  assert.deepEqual(a, b);
  const shifted = layout(dialogTree, glyphMetrics, { x: 100, y: 50 });
  assert.deepEqual([shifted.x, shifted.y], [100, 50]);
  assert.equal(findBox(shifted, "title")!.x, 112);
});

test("nodeIds covers every node for replay target checks", () => {
  const ids = nodeIds(dialogTree);
  assert.deepEqual(
    [...ids].sort(),
    ["btn-cancel", "btn-ok", "buttons", "dialog", "status", "title"],
  );
});
