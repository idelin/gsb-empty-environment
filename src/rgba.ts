/**
 * 自建 RGBA 像素缓冲 -> 二值掩码。
 * 纯手写实现，不依赖 Canvas 滤镜或任何图像处理库。
 */

import { LabelMap, type Connectivity } from "./label-map.ts";

export type RGBABuffer = Uint8Array | Uint8ClampedArray;

/** 判定某个 RGBA 像素是否为前景。 */
export type ForegroundPredicate = (r: number, g: number, b: number, a: number) => boolean;

/**
 * 默认前景判定：不透明（alpha > 0）且偏暗（Rec.601 亮度 < 128）。
 * 即“透明/白色背景上的深色像素”视为前景。
 */
export const defaultForeground: ForegroundPredicate = (r, g, b, a) =>
  a > 0 && (r * 299 + g * 587 + b * 114) / 1000 < 128;

/** 把 RGBA 缓冲转成 0/1 掩码。 */
export function maskFromRGBA(
  data: RGBABuffer,
  width: number,
  height: number,
  predicate: ForegroundPredicate = defaultForeground,
): Uint8Array {
  if (data.length !== width * height * 4) {
    throw new RangeError(`expected ${width * height * 4} bytes, got ${data.length}`);
  }
  const mask = new Uint8Array(width * height);
  for (let i = 0; i < width * height; i++) {
    const o = i * 4;
    mask[i] = predicate(data[o], data[o + 1], data[o + 2], data[o + 3]) ? 1 : 0;
  }
  return mask;
}

/** 直接从 RGBA 缓冲构建 LabelMap。 */
export function labelMapFromRGBA(
  data: RGBABuffer,
  width: number,
  height: number,
  connectivity: Connectivity = 4,
  predicate: ForegroundPredicate = defaultForeground,
): LabelMap {
  return LabelMap.fromMask(maskFromRGBA(data, width, height, predicate), width, height, connectivity);
}

/**
 * 构造一幅 RGBA 小图：透明背景 + 指定坐标的前景像素。
 * 仅用于测试与演示，等价于手工逐像素填充，不借助任何图像库。
 */
export function makeRGBA(
  width: number,
  height: number,
  foregroundPixels: ReadonlyArray<readonly [number, number]>,
  fg: readonly [number, number, number, number] = [0, 0, 0, 255],
): Uint8ClampedArray {
  const data = new Uint8ClampedArray(width * height * 4);
  for (const [x, y] of foregroundPixels) {
    const o = (y * width + x) * 4;
    data[o] = fg[0];
    data[o + 1] = fg[1];
    data[o + 2] = fg[2];
    data[o + 3] = fg[3];
  }
  return data;
}

/** 把标签图渲染成 ASCII：背景为 "."，前景为分量 ID 的 36 进制字符。 */
export function renderASCII(map: LabelMap): string {
  const rows: string[] = [];
  for (let y = 0; y < map.height; y++) {
    let row = "";
    for (let x = 0; x < map.width; x++) {
      const id = map.labelAt(x, y);
      row += id === 0 ? "." : id.toString(36);
    }
    rows.push(row);
  }
  return rows.join("\n");
}
