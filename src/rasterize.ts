/**
 * 栅格化：整个库中唯一允许整数取整的地方。
 *
 * 所有几何/变换计算都保持 float64；只有当结果需要落到
 * 离散像素网格（绘制、写文件、与整数 API 对接）时才调用本模块。
 */

import type { Point, Rect } from "./geometry.ts";

export interface IntPoint {
  x: number;
  y: number;
}

/** 整数半开区间矩形 [x0, x1) × [y0, y1) */
export interface IntRect {
  x0: number;
  y0: number;
  x1: number;
  y1: number;
}

/** 点栅格化：四舍五入到最近的边界格点 */
export function rasterizePoint(p: Point): IntPoint {
  return { x: Math.round(p.x), y: Math.round(p.y) };
}

export type RectRasterizeMode =
  /** 覆盖模式：下取整左上角、上取整右下角，保证覆盖矩形触及的所有像素 */
  | "cover"
  /** 最近模式：四个边界分别四舍五入，可能改变宽高 */
  | "nearest";

/** 矩形栅格化为整数半开区间 */
export function rasterizeRect(r: Rect, mode: RectRasterizeMode = "cover"): IntRect {
  if (mode === "cover") {
    return {
      x0: Math.floor(r.x0),
      y0: Math.floor(r.y0),
      x1: Math.ceil(r.x1),
      y1: Math.ceil(r.y1),
    };
  }
  return {
    x0: Math.round(r.x0),
    y0: Math.round(r.y0),
    x1: Math.round(r.x1),
    y1: Math.round(r.y1),
  };
}
