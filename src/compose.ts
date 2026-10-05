import type { ExactPremult, Rgba8 } from './color.ts';
import {
  fromPremult8,
  fromStraight8,
  over,
  quantizePremult8,
  quantizeStraight8,
  rgba8Equals,
} from './color.ts';

export type ColorFormat = 'straight' | 'premultiplied';

export interface Layer {
  format: ColorFormat;
  color: Rgba8;
}

export interface QuantizedResult {
  premultiplied: Rgba8;
  straight: Rgba8;
}

export function toExact(layer: Layer): ExactPremult {
  return layer.format === 'straight' ? fromStraight8(layer.color) : fromPremult8(layer.color);
}

// layers[0] is the bottom of the stack; each next layer is composited over it.
export function composeExact(layers: Layer[]): ExactPremult {
  if (layers.length === 0) throw new Error('Need at least one layer');
  let acc = toExact(layers[0]);
  for (let i = 1; i < layers.length; i += 1) {
    acc = over(toExact(layers[i]), acc);
  }
  return acc;
}

function finalize(exact: ExactPremult): QuantizedResult {
  return { premultiplied: quantizePremult8(exact), straight: quantizeStraight8(exact) };
}

// One-shot: compose the whole stack with exact rationals, quantize once.
export function composeOneShot(layers: Layer[]): QuantizedResult {
  return finalize(composeExact(layers));
}

// Grouped: compose each contiguous group exactly, quantize the group result to
// 8-bit premultiplied, then feed it into the next round; quantize at the end.
export function composeGrouped(layers: Layer[], groups: number[]): QuantizedResult {
  const total = groups.reduce((s, g) => s + g, 0);
  if (groups.some((g) => !Number.isInteger(g) || g < 1) || total !== layers.length) {
    throw new Error(`Groups ${JSON.stringify(groups)} do not partition ${layers.length} layers`);
  }
  // A single group spans the whole stack, so it is one-shot compositing:
  // quantizing its result before the final quantization would be spurious.
  if (groups.length === 1) return finalize(composeExact(layers));
  let acc: ExactPremult | null = null;
  let offset = 0;
  for (const size of groups) {
    const groupLayers = layers.slice(offset, offset + size);
    offset += size;
    const groupExact = composeExact(groupLayers);
    const groupQuantized = fromPremult8(quantizePremult8(groupExact));
    acc = acc === null ? groupQuantized : over(groupQuantized, acc);
  }
  return finalize(acc as ExactPremult);
}

export function resultsDiffer(x: QuantizedResult, y: QuantizedResult): boolean {
  return !rgba8Equals(x.premultiplied, y.premultiplied) || !rgba8Equals(x.straight, y.straight);
}
