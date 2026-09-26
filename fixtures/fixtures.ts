import type { GlyphMetricTable, InlineNode } from '../src/index.ts';

// ---- 字形度量夹具(代替浏览器布局引擎) ----

/** v1:拉丁字母/空格宽 1(fallback),省略号宽 2,emoji 更宽 */
export const metricsV1: GlyphMetricTable = {
  version: 1,
  fallbackAdvance: 1,
  clusters: {
    '…': 2,
    '👨‍👩‍👧': 4,
    '🇨🇳': 3,
    '👍🏽': 3,
    '@channel': 8,
    'ש': 1, 'ל': 1, 'ו': 1, 'ם': 1,
  },
};

/** v2:晚到的真实字体度量,字母变宽、省略号变宽 */
export const metricsV2: GlyphMetricTable = {
  version: 2,
  fallbackAdvance: 2,
  clusters: {
    '…': 3,
    '👨‍👩‍👧': 5,
    '🇨🇳': 4,
    '👍🏽': 4,
    '@channel': 10,
    'ש': 2, 'ל': 2, 'ו': 2, 'ם': 2,
  },
};

/** 省略号本身超宽的度量,用于退化测试 */
export const metricsWideEllipsis: GlyphMetricTable = {
  version: 3,
  fallbackAdvance: 1,
  clusters: { '…': 30 },
};

// ---- 节点树夹具 ----

export const longText: InlineNode[] = [
  { kind: 'text', text: 'the quick brown fox jumps over the lazy dog again and again' },
];

export const mixedBidi: InlineNode[] = [
  { kind: 'text', text: 'hello ' },
  { kind: 'text', text: 'שלום', dir: 'rtl' },
  { kind: 'text', text: ' world' },
];

export const linked: InlineNode[] = [
  { kind: 'text', text: 'please ' },
  {
    kind: 'link', linkId: 'L1', children: [
      { kind: 'text', text: 'click here to continue reading' },
    ],
  },
  { kind: 'text', text: ' now' },
];

export const withEntity: InlineNode[] = [
  { kind: 'text', text: 'join ' },
  { kind: 'entity', entityId: 'E1', text: '@channel' },
  { kind: 'text', text: ' today and tomorrow' },
];

export const emojiText: InlineNode[] = [
  { kind: 'text', text: 'a👨‍👩‍👧b🇨🇳c👍🏽d' },
];

/** 两个关键片段互相超容量的文本 */
export const conflictText: InlineNode[] = [
  { kind: 'text', text: 'aaaaaaaaaa bbbbbbbbbb cccccccccc dddddddddd eeeeeeeeee' },
];
