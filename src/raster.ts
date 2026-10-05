/**
 * 栅格化：整个库中唯一允许整数取整的环节。
 * 只有在把连续坐标落到最终像素网格时才调用这里的函数。
 */
import type { Rect, Vec2 } from "./geometry.ts";

/** 整数像素索引矩形，仍是半开区间：[x0, x1) × [y0, y1) 覆盖的像素列/行。 */
export interface IntRect {
  readonly x0: number;
  readonly y0: number;
  readonly x1: number;
  readonly y1: number;
}

/**
 * 连续半开矩形 → 覆盖它的最小整数像素集合。
 * 下边界 floor、上边界 ceil：与矩形有正面积交集的像素全部包含。
 */
export function rasterizeRect(r: Rect): IntRect {
  return {
    x0: Math.floor(r.x0),
    y0: Math.floor(r.y0),
    x1: Math.ceil(r.x1),
    y1: Math.ceil(r.y1),
  };
}

/** 连续点 → 它所在的像素索引（floor 语义，与半开区间约定一致）。 */
export function pixelAt(p: Vec2): { readonly x: number; readonly y: number } {
  return { x: Math.floor(p.x), y: Math.floor(p.y) };
}
