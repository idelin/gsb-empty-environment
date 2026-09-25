// Fixture: node tree + glyph metrics. Note the two columns that share the
// label "Score" but have distinct ids (m1/m2) — identity is by id only.
import type { GroupNode, HeaderState, LeafColumn } from '../src/types.ts';
import { measureLabel, type GlyphMetrics } from '../src/measure.ts';

export const glyphMetrics: GlyphMetrics = {
  defaultWidth: 8,
  padding: 12,
  charWidths: {
    A: 7, B: 7, C: 7, D: 7, E: 6, F: 6, G: 7, H: 7, I: 3, J: 5, K: 7, L: 6,
    M: 9, N: 7, O: 7, P: 7, Q: 7, R: 7, S: 6, T: 6, U: 7, V: 7, W: 9, X: 7,
    Y: 6, Z: 6,
    a: 6, b: 6, c: 5, d: 6, e: 6, f: 3, g: 6, h: 6, i: 2, j: 2, k: 5, l: 2,
    m: 9, n: 6, o: 6, p: 6, q: 6, r: 4, s: 5, t: 3, u: 6, v: 5, w: 8, x: 5,
    y: 5, z: 5,
    '0': 6, '1': 6, '2': 6, '3': 6, '4': 6, '5': 6, '6': 6, '7': 6, '8': 6,
    '9': 6, ' ': 3, '-': 4, '_': 5,
    基: 12, 础: 12, 指: 12, 标: 12, 子: 12, 组: 12, 统: 12, 计: 12,
  },
};

function col(id: string, label: string): LeafColumn {
  return { id, label, width: measureLabel(label, glyphMetrics) };
}

export function fixtureColumns(): Map<string, LeafColumn> {
  const list = [
    col('alpha', 'Alpha'),
    col('beta', 'Beta'),
    col('m1', 'Score'), // same label ...
    col('m2', 'Score'), // ... different id
    col('n1', 'N-1'),
    col('n2', 'N-2'),
    col('solo', 'Solo'),
  ];
  return new Map(list.map((c) => [c.id, c]));
}

export function fixtureTree(): GroupNode {
  return {
    kind: 'group',
    id: 'root',
    label: '',
    children: [
      {
        kind: 'group',
        id: 'basic',
        label: '基础',
        children: [
          { kind: 'leaf', columnId: 'alpha' },
          { kind: 'leaf', columnId: 'beta' },
        ],
      },
      {
        kind: 'group',
        id: 'metrics',
        label: '指标',
        children: [
          { kind: 'leaf', columnId: 'm1' },
          { kind: 'leaf', columnId: 'm2' },
          {
            kind: 'group',
            id: 'nested',
            label: '子组',
            children: [
              { kind: 'leaf', columnId: 'n1' },
              { kind: 'leaf', columnId: 'n2' },
            ],
          },
        ],
      },
      { kind: 'leaf', columnId: 'solo' },
    ],
  };
}

export function fixtureState(): HeaderState {
  return {
    root: fixtureTree(),
    columns: fixtureColumns(),
    hidden: new Set(),
    collapsed: new Set(),
  };
}
