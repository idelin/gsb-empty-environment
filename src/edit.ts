/**
 * 编辑应用层：把一次"在 display 坐标系中发生的编辑"映射回 raw，
 * 净化后重新格式化，并算出新光标（display 坐标系）。
 *
 * 宿主（React/Vue/原生 input）职责：
 *  1. 拦截 beforeinput / paste / keydown，拿到选区与待插入文本；
 *  2. 调用 applyEdit / deleteBackward / deleteForward；
 *  3. 用返回的 display 与 caret 受控地重渲染 input（preventDefault，
 *     不依赖浏览器对 value 的默认修正）。
 */

import { sanitizeWithCaret } from "./sanitize.ts";
import { buildPositionMap } from "./mapping.ts";

export interface EditInput {
  /** 当前合法 raw */
  raw: string;
  /** 选区起点（display 坐标系，字符边界下标） */
  selectionStart: number;
  /** 选区终点（display 坐标系） */
  selectionEnd: number;
  /** 待插入文本（删除时为 ""；粘贴时为剪贴板原文，可含逗号/非法字符） */
  inserted: string;
}

export interface EditResult {
  /** 新的合法 raw（中间态原样保留） */
  raw: string;
  /** 新的显示值，恒等于 formatDisplay(raw) */
  display: string;
  /** 新光标（display 坐标系，折叠选区） */
  caret: number;
}

export function applyEdit(input: EditInput): EditResult {
  const { raw, inserted } = input;
  const map = buildPositionMap(raw);
  const selStart = clamp(input.selectionStart, 0, map.display.length);
  const selEnd = clamp(input.selectionEnd, 0, map.display.length);
  const rStart = map.displayToRaw[Math.min(selStart, selEnd)];
  const rEnd = map.displayToRaw[Math.max(selStart, selEnd)];

  const candidate = raw.slice(0, rStart) + inserted + raw.slice(rEnd);
  const candidateCaret = rStart + inserted.length;

  const { raw: nextRaw, caret: rawCaret } = sanitizeWithCaret(candidate, candidateCaret);
  const nextMap = buildPositionMap(nextRaw);
  return {
    raw: nextRaw,
    display: nextMap.display,
    caret: nextMap.rawToDisplay[rawCaret],
  };
}

/** 退格：删除光标前一个 raw 字符（自动跨过千位逗号）。 */
export function deleteBackward(raw: string, caretDisplay: number): EditResult {
  const map = buildPositionMap(raw);
  const rawCaret = map.displayToRaw[clamp(caretDisplay, 0, map.display.length)];
  if (rawCaret === 0) {
    return { raw, display: map.display, caret: map.rawToDisplay[0] };
  }
  return applyEdit({
    raw,
    selectionStart: map.rawToDisplay[rawCaret - 1],
    selectionEnd: map.rawToDisplay[rawCaret],
    inserted: "",
  });
}

/** 前删：删除光标后一个 raw 字符（自动跨过千位逗号）。 */
export function deleteForward(raw: string, caretDisplay: number): EditResult {
  const map = buildPositionMap(raw);
  const rawCaret = map.displayToRaw[clamp(caretDisplay, 0, map.display.length)];
  if (rawCaret === raw.length) {
    return { raw, display: map.display, caret: map.rawToDisplay[rawCaret] };
  }
  return applyEdit({
    raw,
    selectionStart: map.rawToDisplay[rawCaret],
    selectionEnd: map.rawToDisplay[rawCaret + 1],
    inserted: "",
  });
}

function clamp(value: number, min: number, max: number): number {
  return value < min ? min : value > max ? max : value;
}
