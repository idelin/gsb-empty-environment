/**
 * 连续编辑核对（不依赖任何浏览器行为）：
 * 用带原始位置标记（每字符唯一 id）的参考模型 MarkedModel，
 * 与库实现并行执行同一串随机编辑（输入 / 选区替换 / 退格 / 前删 / 粘贴），
 * 每一步交叉核对：
 *   1. 存活字符序列一致（result.raw === model.raw）；
 *   2. 光标落点一致（result 光标换算回 raw 边界 === model.caret）；
 *   3. 不变量：raw 恒合法、display === formatDisplay(raw)、
 *      display 可净化还原为 raw、raw 边界映射可精确往返。
 */

import { test } from "node:test";
import assert from "node:assert/strict";
import { applyEdit, deleteBackward, deleteForward } from "../src/edit.ts";
import type { EditResult } from "../src/edit.ts";
import { buildPositionMap } from "../src/mapping.ts";
import { formatDisplay } from "../src/format.ts";
import { isValidRaw, sanitizeRaw } from "../src/sanitize.ts";
import { MarkedModel } from "./model.ts";

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const ALPHABET = "0123456789-.,abc ￥元";

function randomText(rand: () => number): string {
  const len = Math.floor(rand() * 7);
  let s = "";
  for (let i = 0; i < len; i++) {
    s += ALPHABET[Math.floor(rand() * ALPHABET.length)];
  }
  return s;
}

function checkInvariants(result: EditResult, step: number): void {
  assert.ok(isValidRaw(result.raw), `step ${step}: raw 非法 ${result.raw}`);
  assert.equal(result.display, formatDisplay(result.raw), `step ${step}: display`);
  assert.equal(sanitizeRaw(result.display), result.raw, `step ${step}: 还原`);
  assert.ok(result.caret >= 0 && result.caret <= result.display.length);
  const map = buildPositionMap(result.raw);
  for (let i = 0; i <= result.raw.length; i++) {
    assert.equal(map.displayToRaw[map.rawToDisplay[i]], i, `step ${step}: 往返 @${i}`);
  }
}

for (const seed of [1, 7, 42, 2026]) {
  test(`连续编辑模糊核对 seed=${seed}`, () => {
    const rand = mulberry32(seed);
    const model = MarkedModel.fromRaw("");

    for (let step = 0; step < 500; step++) {
      const raw = model.raw;
      const map = buildPositionMap(raw);
      const op = rand();
      let result: EditResult;

      if (op < 0.4) {
        // 光标处输入 / 粘贴随机文本
        const text = randomText(rand);
        const caretDisplay = map.rawToDisplay[model.caret];
        result = applyEdit({
          raw,
          selectionStart: caretDisplay,
          selectionEnd: caretDisplay,
          inserted: text,
        });
        model.applyRawEdit(model.caret, model.caret, text);
      } else if (op < 0.65) {
        // 随机选区替换
        const a = Math.floor(rand() * (raw.length + 1));
        const b = Math.floor(rand() * (raw.length + 1));
        const rStart = Math.min(a, b);
        const rEnd = Math.max(a, b);
        const text = randomText(rand);
        result = applyEdit({
          raw,
          selectionStart: map.rawToDisplay[rStart],
          selectionEnd: map.rawToDisplay[rEnd],
          inserted: text,
        });
        model.applyRawEdit(rStart, rEnd, text);
      } else if (op < 0.85) {
        // 退格
        const caretDisplay = map.rawToDisplay[model.caret];
        result = deleteBackward(raw, caretDisplay);
        if (model.caret > 0) model.applyRawEdit(model.caret - 1, model.caret, "");
      } else {
        // 前删
        const caretDisplay = map.rawToDisplay[model.caret];
        result = deleteForward(raw, caretDisplay);
        if (model.caret < raw.length) model.applyRawEdit(model.caret, model.caret + 1, "");
      }

      // 1. 存活字符序列一致
      assert.equal(result.raw, model.raw, `step ${step}: raw 分叉`);
      // 2. 光标落点一致（display 光标换算回 raw 边界）
      const nextMap = buildPositionMap(result.raw);
      assert.equal(
        nextMap.displayToRaw[result.caret],
        model.caret,
        `step ${step}: 光标分叉`,
      );
      // 3. 不变量
      checkInvariants(result, step);
    }
  });
}
