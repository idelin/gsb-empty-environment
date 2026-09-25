/**
 * Fixtures: node trees and grapheme metrics. Everything geometric is given
 * here explicitly - the browser layout engine is not involved.
 */

import type { Metrics, SpanNode, TreeNode } from './types.ts';

/** Every cluster one unit wide, except overrides keyed by the cluster text. */
export function fixedMetrics(overrides: Record<string, number> = {}): Metrics {
  return {
    widthOf(cluster: string): number {
      return overrides[cluster] ?? 1;
    },
  };
}

/**
 * a b | A B X Y | c
 *  l0   l1  l1 2 2  l0
 * Logical text: "abABXYc" with an ltr (level 2) island "XY" inside rtl.
 */
export function nestedTree(): SpanNode {
  return {
    id: 'root',
    children: [
      { id: 't-ltr1', text: 'ab', level: 0 },
      {
        id: 's-rtl',
        level: 1,
        children: [
          { id: 't-rtl', text: 'AB', level: 1 },
          { id: 't-ltr2', text: 'XY', level: 2 },
        ],
      },
      { id: 't-ltr3', text: 'c', level: 0 },
    ],
  };
}

/** Plain rtl paragraph of 5 clusters: A B C D E at level 1. */
export function rtlTree(): TreeNode {
  return { id: 'root', level: 1, children: [{ id: 't', text: 'ABCDE', level: 1 }] };
}

/** Grapheme-atomicity fixture: ZWJ family + flag are each single clusters. */
export function graphemeTree(): TreeNode {
  return {
    id: 'root',
    level: 0,
    children: [{ id: 't', text: 'x👨‍👩‍👧🇺🇳y', level: 0 }],
  };
}

/** Two paragraphs: "ab" then "cd". */
export function paragraphsTree(): TreeNode {
  return {
    id: 'root',
    level: 0,
    children: [{ id: 't', text: 'ab\ncd', level: 0 }],
  };
}
