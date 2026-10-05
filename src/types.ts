/**
 * 公共类型定义。
 *
 * 坐标约定：
 * - 掩码像素 (x, y) 占据正方形区域 [x, x+1] × [y, y+1]，y 轴向下（图像坐标系）。
 * - 轮廓顶点使用像素角点（格点）整数坐标，即“像素边坐标”。
 * - 轮廓沿裂缝（crack）追踪，前景始终位于行进方向左侧。
 * - 因此外边界的有向面积为负（y 向下坐标系），孔洞边界为正。
 */

/** 整数格点（像素角点）。 */
export interface Point {
  x: number;
  y: number;
}

/** 二值掩码，行主序，0 为背景，非 0 为前景。 */
export interface Mask {
  width: number;
  height: number;
  data: Uint8Array;
}

/** 一条闭合轮廓。 */
export interface Contour {
  /** 按追踪顺序排列的顶点（闭合，起点不在末尾重复）。 */
  vertices: Point[];
  /** 二倍有向面积（鞋带公式，整数）。外边界为负，孔洞为正。 */
  signedArea2: number;
  /** 是否为孔洞边界。 */
  isHole: boolean;
}

/** 层级树节点，与 contours 数组按下标对应。 */
export interface ContourNode {
  /** 对应 contours 数组的下标。 */
  index: number;
  /** 父轮廓下标，-1 表示根（最外层外边界）。 */
  parent: number;
  /** 子轮廓下标列表。 */
  children: number[];
  /** 嵌套深度：0 = 外边界，1 = 孔洞，2 = 孔中岛，依此类推。 */
  depth: number;
}

/** 层级森林。 */
export interface Hierarchy {
  nodes: ContourNode[];
  /** 最外层轮廓（深度 0）的下标列表。 */
  roots: number[];
}

/** 提取结果：轮廓集合 + 层级森林。 */
export interface ContourSet {
  contours: Contour[];
  nodes: ContourNode[];
  /** 最外层轮廓（深度 0）的下标列表。 */
  roots: number[];
}
