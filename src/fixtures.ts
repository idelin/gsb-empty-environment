// Fixtures: node tree and glyph metrics are GIVEN (as if measured elsewhere).
// The core layout computation below consumes them without any browser layout engine.

export interface GlyphMetrics {
  /** advance width per character, in px */
  readonly advance: Readonly<Record<string, number>>;
  readonly fallbackAdvance: number;
  readonly lineHeight: number;
  readonly spaceAdvance: number;
}

export interface NodeTree {
  readonly id: string;
  readonly kind: 'document' | 'paragraph';
  readonly text?: string;
  readonly children?: readonly NodeTree[];
}

export const glyphMetrics: GlyphMetrics = {
  lineHeight: 18,
  spaceAdvance: 4,
  fallbackAdvance: 8,
  advance: {
    a: 7, b: 7, c: 6, d: 7, e: 7, f: 4, g: 7, h: 7, i: 3, j: 3, k: 6, l: 3, m: 11,
    n: 7, o: 7, p: 7, q: 7, r: 4, s: 6, t: 4, u: 7, v: 6, w: 9, x: 6, y: 6, z: 6,
    A: 8, B: 8, C: 9, D: 9, E: 7, F: 7, G: 9, H: 9, I: 3, J: 6, K: 8, L: 7, M: 11,
    N: 9, O: 9, P: 8, Q: 9, R: 8, S: 8, T: 7, U: 9, V: 8, W: 12, X: 8, Y: 8, Z: 8,
    '0': 7, '1': 7, '2': 7, '3': 7, '4': 7, '5': 7, '6': 7, '7': 7, '8': 7, '9': 7,
    '.': 3, ',': 3, '!': 4, '?': 6, '-': 4, '_': 7, ':': 3, ';': 3, "'": 3, '"': 5,
    ' ': 4,
  },
};

export function makeDocumentFixture(): NodeTree {
  return {
    id: 'doc',
    kind: 'document',
    children: [
      { id: 'p1', kind: 'paragraph', text: 'hello world' },
      { id: 'p2', kind: 'paragraph', text: 'spec driven ui' },
      { id: 'p3', kind: 'paragraph', text: '' },
    ],
  };
}

export function findNode(tree: NodeTree, id: string): NodeTree | undefined {
  if (tree.id === id) return tree;
  for (const child of tree.children ?? []) {
    const hit = findNode(child, id);
    if (hit) return hit;
  }
  return undefined;
}

export function replaceNodeText(tree: NodeTree, id: string, text: string): NodeTree {
  if (tree.id === id) return { ...tree, text };
  if (!tree.children) return tree;
  return { ...tree, children: tree.children.map((c) => replaceNodeText(c, id, text)) };
}
