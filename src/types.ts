/** 二维栅格：行主序存储，rows x cols。 */
export interface Grid {
  rows: number;
  cols: number;
  /** 长度 rows*cols，索引 r*cols+c。 */
  data: Float64Array;
}

/** 数字高程模型。 */
export interface DEM extends Grid {
  /** 栅格单元边长（米）。 */
  cellSize: number;
}

/** 降雨量时态序列：rainfallMmPerTick[t] 为第 t 个 tick 的降雨量（毫米）。 */
export interface RainfallSeries {
  /** 每个 tick 的降雨量（mm/tick），可超出模拟时长（超出部分忽略）。 */
  mmPerTick: readonly number[];
}

/** 单个栅格单元在某 tick 结束时的状态快照。 */
export interface CellState {
  row: number;
  col: number;
  /** 地表高程（米）。 */
  elevation: number;
  /** 积水深度（米）。 */
  waterDepth: number;
  /** 水面高程 = elevation + waterDepth。 */
  waterLevel: number;
}

/** 某一历史 tick 的完整快照。 */
export interface TickSnapshot {
  tick: number;
  /** 积水深度栅格（米）。 */
  depth: Float64Array;
}

/** 平面坐标点（栅格坐标系，单位：米，原点为栅格左上角）。 */
export interface Point {
  x: number;
  y: number;
}

/** 连通淹没区域及其边界多边形。 */
export interface FloodedRegion {
  /** 区域标识（按发现顺序编号）。 */
  id: number;
  /** 区域内栅格单元数。 */
  cellCount: number;
  /** 区域面积（平方米）。 */
  areaSqm: number;
  /** 区域最大积水深度（米）。 */
  maxDepth: number;
  /** 外边界环（逆时针/顺时针由坐标系决定，首尾不重复闭合点）。 */
  outerRing: Point[];
  /** 内部孔洞边界环列表。 */
  holes: Point[][];
}

/** 道路：折线，顶点为栅格坐标（米）。 */
export interface Road {
  id: string;
  /** 折线顶点。 */
  vertices: Point[];
}

/** 道路通行断点：某 tick 时道路被水体阻断的位置。 */
export interface RoadBreakpoint {
  roadId: string;
  tick: number;
  /** 断点位置（栅格坐标，米）。 */
  position: Point;
  /** 所在栅格单元。 */
  cell: { row: number; col: number };
  /** 该处积水深度（米）。 */
  waterDepth: number;
  /** 断点所属的连通淹没区域 id（若有）。 */
  regionId: number | null;
}
