/**
 * Brute-force oracle used to verify the repairer on short token strings.
 *
 * Enumerates every canonical edit sequence (non-decreasing indices) by
 * increasing cost, and — crucially — collects *all* parseable sequences at
 * the minimum cost instead of stopping at the first one, then returns the
 * lexicographically smallest. Parseability is decided by the independent
 * recursive-descent parser in parser.ts.
 */

import type { TokenKind } from './tokens.ts';
import { TERMINALS } from './tokens.ts';
import { parses } from './parser.ts';
import type { Edit } from './edits.ts';
import { compareEditSeqs } from './edits.ts';

export interface BruteForceResult {
  cost: number;
  edits: Edit[];
}

export function bruteForceRepair(input: TokenKind[], maxCost: number): BruteForceResult | null {
  for (let k = 0; k <= maxCost; k++) {
    const found: Edit[][] = [];
    enumerate(input, 0, [], k, found);
    if (found.length > 0) {
      found.sort(compareEditSeqs);
      return { cost: k, edits: found[0] };
    }
  }
  return null;
}

/**
 * Collect every parseable canonical edit sequence of exactly `target` edits.
 * Matching the current token is free and always allowed; edits are only
 * appended while fewer than `target` have been made.
 */
function enumerate(
  input: TokenKind[],
  pos: number,
  edits: Edit[],
  target: number,
  found: Edit[][],
): void {
  if (pos < input.length) {
    // Match the current token (free).
    enumerate(input, pos + 1, edits, target, found);
  } else if (edits.length === target && parses(applyToKinds(input, edits))) {
    found.push(edits);
  }
  if (edits.length < target) {
    // Insert any terminal before position `pos`.
    for (const kind of TERMINALS) {
      enumerate(input, pos, [...edits, { type: 'insert', index: pos, kind }], target, found);
    }
    if (pos < input.length) {
      // Delete the current token.
      enumerate(input, pos + 1, [...edits, { type: 'delete', index: pos, kind: input[pos] }], target, found);
    }
  }
}

function applyToKinds(input: TokenKind[], edits: Edit[]): TokenKind[] {
  const out: TokenKind[] = [];
  let i = 0;
  for (const edit of edits) {
    while (i < edit.index) out.push(input[i++]);
    if (edit.type === 'insert') out.push(edit.kind);
    else i++;
  }
  while (i < input.length) out.push(input[i++]);
  return out;
}
