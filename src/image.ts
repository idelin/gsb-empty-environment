/**
 * 基础 RGBA 图像类型与“独立参考实现”。
 *
 * 本文件中的 downsampleReference / referencePyramid 是刻意与瓦片管线
 * （src/pyramid.ts）分离的逐像素直接实现：不经过瓦片、不经过缓存，
 * 用于交叉核验瓦片金字塔在奇数尺寸、多层传播下的正确性。
 *
 * 降采样定义（两处实现共同遵守）：
 *   输出像素 (ox, oy) 覆盖输入块 [2ox, 2ox+2) × [2oy, 2oy+2)，
 *   块在图像边缘处被裁剪，权重 = 实际覆盖的输入像素数（1/2/4），
 *   即边缘不补零、不变暗。层尺寸按 ceil(n / 2^level) 取整。
 */

export interface RGBAImage {
  width: number;
  height: number;
  /** 长度 = width * height * 4，RGBA 顺序 */
  data: Uint8Array;
}

export type PixelFill = (x: number, y: number) => readonly [number, number, number, number];

export function createImage(width: number, height: number, fill?: PixelFill): RGBAImage {
  const data = new Uint8Array(width * height * 4);
  const img: RGBAImage = { width, height, data };
  if (fill) {
    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width; x++) {
        const [r, g, b, a] = fill(x, y);
        setPixel(img, x, y, r, g, b, a);
      }
    }
  }
  return img;
}

export function getPixel(img: RGBAImage, x: number, y: number): readonly [number, number, number, number] {
  const i = (y * img.width + x) * 4;
  return [img.data[i], img.data[i + 1], img.data[i + 2], img.data[i + 3]];
}

export function setPixel(img: RGBAImage, x: number, y: number, r: number, g: number, b: number, a: number): void {
  const i = (y * img.width + x) * 4;
  img.data[i] = r;
  img.data[i + 1] = g;
  img.data[i + 2] = b;
  img.data[i + 3] = a;
}

/** 第 level 层的尺寸定义：ceil(n / 2^level) */
export function levelDim(n: number, level: number): number {
  return Math.ceil(n / 2 ** level);
}

/**
 * 独立参考实现：整图逐像素 2×2 盒式降采样一层。
 * 边缘块按实际覆盖像素数加权（不补零）。
 */
export function downsampleReference(src: RGBAImage): RGBAImage {
  const width = Math.ceil(src.width / 2);
  const height = Math.ceil(src.height / 2);
  const data = new Uint8Array(width * height * 4);
  for (let oy = 0; oy < height; oy++) {
    for (let ox = 0; ox < width; ox++) {
      const x0 = ox * 2;
      const y0 = oy * 2;
      const x1 = Math.min(x0 + 2, src.width);
      const y1 = Math.min(y0 + 2, src.height);
      const n = (x1 - x0) * (y1 - y0);
      let r = 0, g = 0, b = 0, a = 0;
      for (let y = y0; y < y1; y++) {
        for (let x = x0; x < x1; x++) {
          const i = (y * src.width + x) * 4;
          r += src.data[i];
          g += src.data[i + 1];
          b += src.data[i + 2];
          a += src.data[i + 3];
        }
      }
      const o = (oy * width + ox) * 4;
      data[o] = Math.round(r / n);
      data[o + 1] = Math.round(g / n);
      data[o + 2] = Math.round(b / n);
      data[o + 3] = Math.round(a / n);
    }
  }
  return { width, height, data };
}

/** 独立参考金字塔：从基准图逐层整图降采样，直到 1×1。 */
export function referencePyramid(base: RGBAImage): RGBAImage[] {
  const levels: RGBAImage[] = [base];
  while (levels[levels.length - 1].width > 1 || levels[levels.length - 1].height > 1) {
    levels.push(downsampleReference(levels[levels.length - 1]));
  }
  return levels;
}
