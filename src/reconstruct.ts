import type { Contour, Mask } from './types.js';

/**
 * 由轮廓重建二值掩码（扫描线奇偶填充）。
 * 在像素中心高度 y + 0.5 处统计竖直边穿越，全程整数运算；
 * 外边界与孔洞交替嵌套，奇偶规则天然还原原图。
 */
export function rasterize(contours: Contour[], width: number, height: number): Mask {
  const data = new Uint8Array(width * height);
  for (let y = 0; y < height; y++) {
    const crossings: number[] = [];
    for (const contour of contours) {
      const vertices = contour.vertices;
      const n = vertices.length;
      for (let i = 0; i < n; i++) {
        const a = vertices[i];
        const b = vertices[(i + 1) % n];
        if (a.x !== b.x) continue;
        const lo = a.y < b.y ? a.y : b.y;
        const hi = a.y < b.y ? b.y : a.y;
        if (lo <= y && y < hi) crossings.push(a.x);
      }
    }
    crossings.sort((p: number, q: number) => p - q);
    for (let k = 0; k + 1 < crossings.length; k += 2) {
      for (let x = crossings[k]; x < crossings[k + 1]; x++) {
        data[y * width + x] = 1;
      }
    }
  }
  return { width, height, data };
}

/** 逐像素比较两个掩码。 */
export function masksEqual(a: Mask, b: Mask): boolean {
  if (a.width !== b.width || a.height !== b.height) return false;
  for (let i = 0; i < a.data.length; i++) {
    if ((a.data[i] === 0 ? 0 : 1) !== (b.data[i] === 0 ? 0 : 1)) return false;
  }
  return true;
}
