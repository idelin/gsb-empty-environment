/**
 * 一次性合成 vs 分组后量化的比较。
 *
 * - 一次性（one-shot）：所有图层精确合成，最后只量化一次。
 * - 分组（grouped）：每组内部精确合成 -> 量化为 8 位 -> 重新解析，
 *   组间再精确合成，最后再量化一次。中间量化丢失的信息即差异来源。
 */

import {
  composeOver,
  fromPremultiplied8,
  fromStraight8,
  toPremultiplied8,
  toStraight8,
  type ExactColor,
  type RGBA8,
} from "./color.ts";

export type InputFormat = "straight" | "premultiplied";

export interface Layer {
  format: InputFormat;
  color: RGBA8;
}

export function parseLayer(layer: Layer): ExactColor {
  return layer.format === "straight"
    ? fromStraight8(layer.color)
    : fromPremultiplied8(layer.color);
}

/** 一次性合成：layers[0] 为最底层，精确合成后量化一次。 */
export function compositeOneShot(layers: Layer[]): RGBA8 {
  return toStraight8(composeOver(layers.map(parseLayer)));
}

export type IntermediateFormat = "straight" | "premultiplied";

/**
 * 分组合成：groupSizes 之和必须等于 layers.length。
 * 每组精确合成后量化为 8 位（默认 straight），再作为新图层参与合成。
 */
export function compositeGrouped(
  layers: Layer[],
  groupSizes: number[],
  intermediate: IntermediateFormat = "straight",
): RGBA8 {
  const total = groupSizes.reduce((s, n) => s + n, 0);
  if (total !== layers.length || groupSizes.some((n) => n < 1)) {
    throw new RangeError(
      `groupSizes ${JSON.stringify(groupSizes)} does not partition ${layers.length} layers`,
    );
  }
  const quantizedGroups: Layer[] = [];
  let offset = 0;
  for (const size of groupSizes) {
    const exact = composeOver(layers.slice(offset, offset + size).map(parseLayer));
    quantizedGroups.push(
      intermediate === "straight"
        ? { format: "straight", color: toStraight8(exact) }
        : { format: "premultiplied", color: toPremultiplied8(exact) },
    );
    offset += size;
  }
  return compositeOneShot(quantizedGroups);
}

export function diffChannels(x: RGBA8, y: RGBA8): string[] {
  const out: string[] = [];
  if (x.r !== y.r) out.push(`r: ${x.r} != ${y.r}`);
  if (x.g !== y.g) out.push(`g: ${x.g} != ${y.g}`);
  if (x.b !== y.b) out.push(`b: ${x.b} != ${y.b}`);
  if (x.a !== y.a) out.push(`a: ${x.a} != ${y.a}`);
  return out;
}
