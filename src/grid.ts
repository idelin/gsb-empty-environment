/**
 * TileGrid：将地理空间按等经纬度瓦片网格离散化。
 * 负责 (lng, lat) <-> cellIndex 的双向映射与网格中心计算。
 */

export interface GridBounds {
  minLng: number;
  minLat: number;
  maxLng: number;
  maxLat: number;
}

export interface GridConfig extends GridBounds {
  /** 横向（经度方向）网格数 */
  cols: number;
  /** 纵向（纬度方向）网格数 */
  rows: number;
}

export class TileGrid {
  readonly bounds: GridBounds;
  readonly cols: number;
  readonly rows: number;
  readonly cellCount: number;
  private readonly cellWidth: number;  // 单格经度跨度（度）
  private readonly cellHeight: number; // 单格纬度跨度（度）

  constructor(config: GridConfig) {
    if (config.cols <= 0 || config.rows <= 0) {
      throw new Error('cols/rows must be positive');
    }
    if (config.maxLng <= config.minLng || config.maxLat <= config.minLat) {
      throw new Error('invalid bounds');
    }
    this.bounds = {
      minLng: config.minLng,
      minLat: config.minLat,
      maxLng: config.maxLng,
      maxLat: config.maxLat,
    };
    this.cols = config.cols;
    this.rows = config.rows;
    this.cellCount = config.cols * config.rows;
    this.cellWidth = (config.maxLng - config.minLng) / config.cols;
    this.cellHeight = (config.maxLat - config.minLat) / config.rows;
  }

  /** 经纬度 -> 网格编号；越界返回 -1 */
  cellIndexOf(lng: number, lat: number): number {
    const { minLng, minLat, maxLng, maxLat } = this.bounds;
    if (lng < minLng || lng >= maxLng || lat < minLat || lat >= maxLat) {
      return -1;
    }
    const col = Math.min(this.cols - 1, Math.floor((lng - minLng) / this.cellWidth));
    const row = Math.min(this.rows - 1, Math.floor((lat - minLat) / this.cellHeight));
    return row * this.cols + col;
  }

  /** 网格编号 -> 中心经纬度 */
  cellCenter(cellIndex: number): { lng: number; lat: number } {
    const col = cellIndex % this.cols;
    const row = Math.floor(cellIndex / this.cols);
    return {
      lng: this.bounds.minLng + (col + 0.5) * this.cellWidth,
      lat: this.bounds.minLat + (row + 0.5) * this.cellHeight,
    };
  }
}
