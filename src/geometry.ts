/** 二维向量基础原语 */
export interface Vec {
  x: number;
  y: number;
}

/** 向量 OA 与 OB 的叉积（>0 表示 B 在 OA 左侧） */
export function cross(o: Vec, a: Vec, b: Vec): number {
  return (a.x - o.x) * (b.y - o.y) - (a.y - o.y) * (b.x - o.x);
}

/** 多边形有向面积（逆时针为正） */
export function signedArea(poly: Vec[]): number {
  let sum = 0;
  for (let i = 0; i < poly.length; i++) {
    const a = poly[i];
    const b = poly[(i + 1) % poly.length];
    sum += a.x * b.y - b.x * a.y;
  }
  return sum / 2;
}
