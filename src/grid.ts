import { hmacSha256 } from './crypto.ts';

export interface GridCell {
  cx: number;
  cy: number;
}

export function gridCell(lat: number, lng: number, cellSizeDeg: number): GridCell {
  if (!Number.isFinite(lat) || !Number.isFinite(lng)) {
    throw new Error('invalid coordinates');
  }
  if (lat < -90 || lat > 90 || lng < -180 || lng > 180) {
    throw new Error('coordinates out of range');
  }
  return {
    cx: Math.floor(lat / cellSizeDeg),
    cy: Math.floor(lng / cellSizeDeg),
  };
}

export function gridHash(salt: Buffer, cell: GridCell): string {
  return hmacSha256(salt, `grid:${cell.cx}:${cell.cy}`).toString('hex');
}
