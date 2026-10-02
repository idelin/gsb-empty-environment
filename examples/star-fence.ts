import type { GeoFencePolygon, LngLat, Classification } from '../src/geofence.ts';

/**
 * 验收样例：五角星凹多边形围栏，中央镂空一个方形岛屿。
 *
 * 外环：10 个顶点交替（5 个凸尖 + 5 个凹点），逆时针，坐标全部取
 * 整数，保证边界中点等临界点可被精确表示，便于严格边界断言。
 *
 *            (4,18)      (16,18)
 *               \   (10,14)   /
 *   (1,7) -- (8,7) \         / (12,7) -- (19,7)
 *                    \     /
 *                     (10,0)
 *
 * 孔洞：正方形 (8,8)-(12,8)-(12,11)-(8,11)，完全落在星形实体内部。
 */
export const starFence: GeoFencePolygon = {
  outer: [
    { lng: 10, lat: 0 },
    { lng: 12, lat: 7 },
    { lng: 19, lat: 7 },
    { lng: 14, lat: 11 },
    { lng: 16, lat: 18 },
    { lng: 10, lat: 14 },
    { lng: 4, lat: 18 },
    { lng: 6, lat: 11 },
    { lng: 1, lat: 7 },
    { lng: 8, lat: 7 },
  ],
  holes: [
    [
      { lng: 8, lat: 8 },
      { lng: 12, lat: 8 },
      { lng: 12, lat: 11 },
      { lng: 8, lat: 11 },
    ],
  ],
};

/** 五角星 {5/2} 自相交退化多边形（同一批星尖按五角星顺序连线） */
export const pentagramFence: GeoFencePolygon = {
  outer: [
    { lng: 10, lat: 0 },
    { lng: 16, lat: 18 },
    { lng: 1, lat: 7 },
    { lng: 19, lat: 7 },
    { lng: 4, lat: 18 },
  ],
};

/** 蝴蝶结（bowtie）自相交退化多边形 */
export const bowtieFence: GeoFencePolygon = {
  outer: [
    { lng: 0, lat: 0 },
    { lng: 4, lat: 4 },
    { lng: 4, lat: 0 },
    { lng: 0, lat: 4 },
  ],
};

export interface LabeledPoint {
  label: string;
  point: LngLat;
  expected: Classification;
}

/** 验收测试点：覆盖实体、孔洞、外部、顶点、边中点、孔洞边缘 */
export const starTestPoints: LabeledPoint[] = [
  // 实体部分（星臂与中央实体区）
  { label: 'top-right arm (solid)', point: { lng: 13, lat: 14 }, expected: 'inside' },
  { label: 'top-left arm (solid)', point: { lng: 7, lat: 14 }, expected: 'inside' },
  { label: 'bottom arm (solid)', point: { lng: 10, lat: 3 }, expected: 'inside' },
  { label: 'right arm (solid)', point: { lng: 15, lat: 8 }, expected: 'inside' },
  { label: 'left arm (solid)', point: { lng: 5, lat: 8 }, expected: 'inside' },
  { label: 'core below hole (solid)', point: { lng: 10, lat: 7.5 }, expected: 'inside' },
  // 中空孔洞部分
  { label: 'hole center (hollow)', point: { lng: 10, lat: 9.5 }, expected: 'outside' },
  { label: 'hole interior (hollow)', point: { lng: 9, lat: 10 }, expected: 'outside' },
  // 围栏外部
  { label: 'top notch (concave)', point: { lng: 10, lat: 16 }, expected: 'outside' },
  { label: 'between arms, left', point: { lng: 3, lat: 14 }, expected: 'outside' },
  { label: 'between arms, right', point: { lng: 17, lat: 14 }, expected: 'outside' },
  { label: 'far above', point: { lng: 10, lat: 20 }, expected: 'outside' },
  // 边框临界点
  { label: 'outer tip vertex', point: { lng: 10, lat: 0 }, expected: 'boundary' },
  { label: 'concave vertex', point: { lng: 10, lat: 14 }, expected: 'boundary' },
  { label: 'edge midpoint', point: { lng: 11, lat: 3.5 }, expected: 'boundary' },
  { label: 'horizontal edge', point: { lng: 15, lat: 7 }, expected: 'boundary' },
  { label: 'hole edge', point: { lng: 10, lat: 8 }, expected: 'boundary' },
  { label: 'hole vertex', point: { lng: 8, lat: 8 }, expected: 'boundary' },
];
