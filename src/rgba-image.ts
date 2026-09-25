/**
 * Minimal self-contained RGBA image. No canvas, no image libraries:
 * pixels live in a plain Uint8ClampedArray (row-major, 4 bytes per pixel).
 *
 * Foreground predicate (binary interpretation of the RGBA data):
 *   alpha >= ALPHA_THRESHOLD AND average(r, g, b) < LUMINANCE_THRESHOLD
 * i.e. "mostly opaque and dark" pixels are foreground, everything else
 * (transparent, or light) is background.
 */

export const ALPHA_THRESHOLD = 128;
export const LUMINANCE_THRESHOLD = 128;

export class RGBAImage {
  readonly width: number;
  readonly height: number;
  readonly data: Uint8ClampedArray;

  constructor(width: number, height: number, data?: Uint8ClampedArray) {
    if (!Number.isInteger(width) || !Number.isInteger(height) || width <= 0 || height <= 0) {
      throw new RangeError(`invalid image size: ${width}x${height}`);
    }
    this.width = width;
    this.height = height;
    this.data = data ?? new Uint8ClampedArray(width * height * 4);
    if (this.data.length !== width * height * 4) {
      throw new RangeError(
        `RGBA buffer length ${this.data.length} does not match ${width}x${height} image`,
      );
    }
  }

  /**
   * Build an image from ASCII art. Any character listed in `foregroundChars`
   * becomes an opaque black (foreground) pixel; everything else stays
   * fully transparent (background). Rows may have different lengths.
   */
  static fromAscii(rows: string[], foregroundChars = "#X1@"): RGBAImage {
    if (rows.length === 0) throw new RangeError("ascii art needs at least one row");
    const height = rows.length;
    const width = Math.max(...rows.map((row) => row.length));
    const image = new RGBAImage(width, height);
    for (let y = 0; y < height; y++) {
      const row = rows[y];
      for (let x = 0; x < row.length; x++) {
        if (foregroundChars.includes(row[x])) {
          image.setPixel(x, y, 0, 0, 0, 255);
        }
      }
    }
    return image;
  }

  setPixel(x: number, y: number, r: number, g: number, b: number, a: number): void {
    const offset = this.offsetOf(x, y);
    this.data[offset] = r;
    this.data[offset + 1] = g;
    this.data[offset + 2] = b;
    this.data[offset + 3] = a;
  }

  /** Convenience: paint an opaque black (foreground) or transparent (background) pixel. */
  setForeground(x: number, y: number, on: boolean): void {
    if (on) this.setPixel(x, y, 0, 0, 0, 255);
    else this.setPixel(x, y, 0, 0, 0, 0);
  }

  getPixel(x: number, y: number): [number, number, number, number] {
    const offset = this.offsetOf(x, y);
    return [
      this.data[offset],
      this.data[offset + 1],
      this.data[offset + 2],
      this.data[offset + 3],
    ];
  }

  isForeground(x: number, y: number): boolean {
    const offset = this.offsetOf(x, y);
    if (this.data[offset + 3] < ALPHA_THRESHOLD) return false;
    const average =
      (this.data[offset] + this.data[offset + 1] + this.data[offset + 2]) / 3;
    return average < LUMINANCE_THRESHOLD;
  }

  private offsetOf(x: number, y: number): number {
    if (x < 0 || x >= this.width || y < 0 || y >= this.height) {
      throw new RangeError(`pixel out of bounds: (${x}, ${y})`);
    }
    return (y * this.width + x) * 4;
  }
}
