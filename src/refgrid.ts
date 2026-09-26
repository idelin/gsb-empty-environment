import type { Label } from "./raster.ts";

export class RefGrid {
  readonly width: number;
  readonly height: number;
  private px: Label[][];

  constructor(width: number, height: number, fill: Label = 0) {
    this.width = width;
    this.height = height;
    this.px = Array.from({ length: height }, () => new Array<Label>(width).fill(fill));
  }

  readRect(x: number, y: number, w: number, h: number): Label[] {
    const out: Label[] = [];
    for (let ry = 0; ry < h; ry++) for (let rx = 0; rx < w; rx++) out.push(this.px[y + ry][x + rx]);
    return out;
  }

  writeRect(x: number, y: number, w: number, h: number, src: Label | ArrayLike<Label>): void {
    const isArray = typeof src !== "number";
    for (let ry = 0; ry < h; ry++) {
      for (let rx = 0; rx < w; rx++) {
        this.px[y + ry][x + rx] = isArray ? (src as ArrayLike<Label>)[ry * w + rx] : (src as Label);
      }
    }
  }

  applyMask(x: number, y: number, w: number, h: number, mask: ArrayLike<number>, label: Label): void {
    for (let ry = 0; ry < h; ry++) {
      for (let rx = 0; rx < w; rx++) {
        if (mask[ry * w + rx]) this.px[y + ry][x + rx] = label;
      }
    }
  }

  translateRegion(x: number, y: number, w: number, h: number, dx: number, dy: number): void {
    const src = this.readRect(x, y, w, h);
    const nx = x + dx;
    const ny = y + dy;
    for (let ry = 0; ry < h; ry++) {
      for (let rx = 0; rx < w; rx++) {
        const tx = nx + rx;
        const ty = ny + ry;
        if (tx >= 0 && tx < this.width && ty >= 0 && ty < this.height) {
          this.px[ty][tx] = src[ry * w + rx];
        }
      }
    }
  }

  all(): Label[] {
    return this.readRect(0, 0, this.width, this.height);
  }
}
