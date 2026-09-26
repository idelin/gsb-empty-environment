import type { ColorSpace } from './color.ts';

export type BlendOp = 'source-over' | 'lighter' | 'multiply';

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Layer {
  id: string;
  width: number;
  height: number;
  pixels: Float32Array; // RGBA，0..1，是否预乘由 premultiplied 标记决定
  colorSpace: ColorSpace;
  premultiplied: boolean;
  offsetX: number;
  offsetY: number;
  crop: Rect | null; // 图层像素坐标系内的裁剪
  blend: BlendOp;
  opacity: number;
  inputs: string[]; // 依赖：必须排在这些图层之后合成
  visible: boolean;
}

// 图层在画布上的保守包围盒（裁剪+偏移后与画布求交），null 表示不可见。
export function layerBounds(layer: Layer, canvasW: number, canvasH: number): Rect | null {
  if (!layer.visible) return null;
  const crop = layer.crop ?? { x: 0, y: 0, w: layer.width, h: layer.height };
  const cx = Math.max(0, crop.x);
  const cy = Math.max(0, crop.y);
  const cw = Math.min(layer.width, crop.x + crop.w) - cx;
  const ch = Math.min(layer.height, crop.y + crop.h) - cy;
  if (cw <= 0 || ch <= 0) return null;
  const x = layer.offsetX;
  const y = layer.offsetY;
  const ix = Math.max(0, x);
  const iy = Math.max(0, y);
  const iw = Math.min(canvasW, x + cw) - ix;
  const ih = Math.min(canvasH, y + ch) - iy;
  if (iw <= 0 || ih <= 0) return null;
  return { x: ix, y: iy, w: iw, h: ih };
}
