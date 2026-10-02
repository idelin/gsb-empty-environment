/** Geographic tile grid: discretizes a bounding box into fixed-size cells. */

export interface GridSpec {
  minLat: number;
  minLng: number;
  maxLat: number;
  maxLng: number;
  /** Tile edge length in meters. */
  tileSizeMeters: number;
}

const METERS_PER_DEG_LAT = 111_320;

export class TileGrid {
  readonly spec: GridSpec;
  readonly rows: number;
  readonly cols: number;
  readonly cellCount: number;
  private readonly metersPerDegLng: number;
  private readonly dLat: number; // tile height in degrees
  private readonly dLng: number; // tile width in degrees

  constructor(spec: GridSpec) {
    if (spec.maxLat <= spec.minLat || spec.maxLng <= spec.minLng) {
      throw new Error('invalid bounding box');
    }
    if (!(spec.tileSizeMeters > 0)) {
      throw new Error('tileSizeMeters must be positive');
    }
    this.spec = spec;
    const midLat = (spec.minLat + spec.maxLat) / 2;
    this.metersPerDegLng = METERS_PER_DEG_LAT * Math.cos((midLat * Math.PI) / 180);
    this.dLat = spec.tileSizeMeters / METERS_PER_DEG_LAT;
    this.dLng = spec.tileSizeMeters / this.metersPerDegLng;
    this.rows = Math.ceil((spec.maxLat - spec.minLat) / this.dLat);
    this.cols = Math.ceil((spec.maxLng - spec.minLng) / this.dLng);
    this.cellCount = this.rows * this.cols;
  }

  /** Returns the linear cell index for a coordinate, or -1 when outside the bbox. */
  cellOf(lat: number, lng: number): number {
    const { minLat, minLng, maxLat, maxLng } = this.spec;
    if (lat < minLat || lat >= maxLat || lng < minLng || lng >= maxLng) return -1;
    let row = Math.floor((lat - minLat) / this.dLat);
    let col = Math.floor((lng - minLng) / this.dLng);
    if (row >= this.rows) row = this.rows - 1;
    if (col >= this.cols) col = this.cols - 1;
    return row * this.cols + col;
  }

  /**
   * Returns the geographic center of a cell. Edge tiles are clipped to the
   * bbox, so the returned center is the centroid of the clipped tile and
   * always round-trips through `cellOf`.
   */
  centerOf(cell: number): { lat: number; lng: number } {
    const row = Math.floor(cell / this.cols);
    const col = cell % this.cols;
    const { minLat, minLng, maxLat, maxLng } = this.spec;
    return {
      lat: (minLat + row * this.dLat + Math.min(minLat + (row + 1) * this.dLat, maxLat)) / 2,
      lng: (minLng + col * this.dLng + Math.min(minLng + (col + 1) * this.dLng, maxLng)) / 2,
    };
  }
}
