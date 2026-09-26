/**
 * 夹具：字形度量与节点树全部由数据给出，
 * 核心计算不依赖任何浏览器布局引擎。
 */
import type { FontFace, LayoutNode } from './types.ts';

function glyphs(table: Record<string, number>): Readonly<Record<string, number>> {
  return Object.freeze({ ...table });
}

/** serif v1：旧版本字形度量。 */
export const serifV1: FontFace = Object.freeze({
  id: 'serif',
  family: 'Fixture Serif',
  version: 1,
  unitsPerEm: 1000,
  ascent: 800,
  descent: 200,
  glyphs: glyphs({ H: 720, e: 500, l: 240, o: 560, W: 900, r: 340, d: 560, a: 480, '!': 280, ' ': 250 }),
});

/** serif v2：热更新后的新版本，advance 全面变化，用于验证精确失效。 */
export const serifV2: FontFace = Object.freeze({
  ...serifV1,
  version: 2,
  glyphs: glyphs({ H: 760, e: 520, l: 260, o: 600, W: 940, r: 360, d: 580, a: 500, '!': 300, ' ': 260 }),
});

/** mono v1：另一个字体，验证失效只影响目标字体。 */
export const monoV1: FontFace = Object.freeze({
  id: 'mono',
  family: 'Fixture Mono',
  version: 1,
  unitsPerEm: 1000,
  ascent: 750,
  descent: 250,
  glyphs: glyphs({ o: 600, k: 600, ' ': 600 }),
});

/** 节点树夹具。 */
export const sampleTree: LayoutNode = Object.freeze({
  kind: 'box',
  children: Object.freeze([
    Object.freeze({ kind: 'text', text: 'Hello', style: Object.freeze({ fontId: 'serif', fontSize: 16, letterSpacing: 0 }) }),
    Object.freeze({ kind: 'text', text: 'World!', style: Object.freeze({ fontId: 'serif', fontSize: 16, letterSpacing: 2 }) }),
    Object.freeze({
      kind: 'box',
      children: Object.freeze([
        Object.freeze({ kind: 'text', text: 'ok', style: Object.freeze({ fontId: 'mono', fontSize: 12, letterSpacing: 0 }) }),
      ]),
    }),
  ]),
});
