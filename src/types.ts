/** 经纬度坐标（WGS84，度）。 */
export interface LatLon {
  lat: number;
  lon: number;
}

/** 带时间戳的 GPS 观测点，timestamp 为毫秒。 */
export interface GpsPoint extends LatLon {
  timestamp: number;
}

/** 路网中的一条有向无差别的路段（两个交叉口之间）。 */
export interface RoadSegment {
  id: number;
  name: string;
  start: LatLon;
  end: LatLon;
  /** 路段长度（米）。 */
  lengthM: number;
  startNode: number;
  endNode: number;
}

/** GPS 点在某条路段上的垂直投影结果。 */
export interface Projection {
  /** 投影点（道路中心线上）。 */
  point: LatLon;
  /** GPS 点到投影点的距离（米）。 */
  distanceM: number;
  /** 投影参数 t ∈ [0,1]，0 在 start，1 在 end。 */
  fraction: number;
  /** 投影点距路段 start 端的沿线距离（米）。 */
  alongM: number;
}

/** HMM 隐状态：某时刻的一个候选匹配（路段 + 投影）。 */
export interface Candidate {
  segmentId: number;
  projection: Projection;
  /** 发射概率的对数。 */
  emissionLogProb: number;
}

/** 一个 GPS 点的最终匹配结果。 */
export interface MatchedPoint {
  original: GpsPoint;
  /** 吸附到道路中心线后的位置。 */
  matched: LatLon;
  segmentId: number;
  segmentName: string;
  /** 原始 GPS 点到匹配点的距离（米）。 */
  snapDistanceM: number;
}
