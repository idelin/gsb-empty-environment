/**
 * Repair edits. A repair is an ordered sequence of token insertions and
 * deletions, each costing 1. Indices always refer to the *original* token
 * stream: `delete` removes the original token at `index`; `insert` places a
 * new token right before original position `index` (0..n). Sequences produced
 * by the repairer are canonical: indices are non-decreasing.
 */

import type { Token, TokenKind } from './tokens.ts';
import { canonicalText } from './tokens.ts';

export interface Edit {
  type: 'insert' | 'delete';
  index: number;
  kind: TokenKind;
}

/**
 * Total order on edits used for the lexicographic tie-break: deletions sort
 * before insertions, then by original index, then by token kind. A repair
 * sequence is compared element-wise; a proper prefix is smaller.
 */
export function editKey(edit: Edit): string {
  const at = String(edit.index).padStart(8, '0');
  return `${edit.type === 'delete' ? 'D' : 'I'}@${at}:${edit.kind}`;
}

export function compareEditSeqs(a: Edit[], b: Edit[]): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    const ka = editKey(a[i]);
    const kb = editKey(b[i]);
    if (ka < kb) return -1;
    if (ka > kb) return 1;
  }
  return a.length - b.length;
}

/** Apply a canonical edit sequence, preserving original tokens (and positions). */
export function applyEdits(input: Token[], edits: Edit[]): Token[] {
  const out: Token[] = [];
  let i = 0;
  for (const edit of edits) {
    while (i < edit.index) out.push(input[i++]);
    if (edit.type === 'insert') {
      out.push({ kind: edit.kind, text: canonicalText(edit.kind), pos: null });
    } else {
      i++; // drop the original token at edit.index
    }
  }
  while (i < input.length) out.push(input[i++]);
  return out;
}

export function formatEdit(edit: Edit): string {
  return edit.type === 'delete'
    ? `delete ${edit.kind} at #${edit.index}`
    : `insert ${edit.kind} before #${edit.index}`;
}
