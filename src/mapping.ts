/**
 * raw 与 display 之间的光标位置映射。
 *
 * 位置一律使用"字符边界下标"：0..str.length，表示光标落在第 i 个字符之前。
 * 映射表对任意合法 raw 满足：
 *   displayToRaw[rawToDisplay[i]] === i   （raw 边界可精确往返）
 * display 中逗号两侧的边界都吸附到同一个 raw 边界（逗号不对应任何 raw 字符），
 * 因此 display 边界不一定能往返 —— 这是有意为之的确定性行为。
 */

import { formatDisplay } from "./format.ts";

export interface PositionMap {
  /** 长度 raw.length + 1；rawToDisplay[i] 为 raw 第 i 边界对应的 display 边界 */
  rawToDisplay: number[];
  /** 长度 display.length + 1；displayToRaw[d] 为 display 第 d 边界对应的 raw 边界 */
  displayToRaw: number[];
  display: string;
}

export function buildPositionMap(raw: string): PositionMap {
  const display = formatDisplay(raw);
  const rawToDisplay: number[] = new Array(raw.length + 1);
  const displayToRaw: number[] = new Array(display.length + 1);
  let r = 0;
  rawToDisplay[0] = 0;
  for (let d = 0; d < display.length; d++) {
    if (display[d] === ",") {
      // 逗号边界：吸附到前一个 raw 字符之后的边界
      displayToRaw[d] = r;
    } else {
      displayToRaw[d] = r;
      r++;
      rawToDisplay[r] = d + 1;
    }
  }
  displayToRaw[display.length] = r;
  return { rawToDisplay, displayToRaw, display };
}

export function rawIndexToDisplayIndex(raw: string, rawIndex: number): number {
  const map = buildPositionMap(raw);
  return map.rawToDisplay[clamp(rawIndex, 0, raw.length)];
}

export function displayIndexToRawIndex(raw: string, displayIndex: number): number {
  const map = buildPositionMap(raw);
  return map.displayToRaw[clamp(displayIndex, 0, map.display.length)];
}

function clamp(value: number, min: number, max: number): number {
  return value < min ? min : value > max ? max : value;
}
