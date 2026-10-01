import type { HyphenationSpec } from './nodes.ts';
import type { BreakOptions } from './types.ts';

/** Hyphenation dictionary fixture (only these words may be broken inside). */
export const HYPHENATION: HyphenationSpec = {
  hyphenation: ['hy', 'phen', 'ation'],
  international: ['in', 'ter', 'na', 'tion', 'al'],
  optimization: ['op', 'ti', 'mi', 'za', 'tion'],
  beautiful: ['beau', 'ti', 'ful'],
  paragraph: ['par', 'a', 'graph'],
};

export const BASE_OPTIONS: BreakOptions = {
  lineWidth: 200,
  widthDiffWeight: 2,
  maxHyphens: 2,
  forbidConsecutiveHyphens: true,
  budget: 1_000_000,
};

export const PARAGRAPHS = {
  simple:
    'The quick brown fox jumps over a lazy dog while beautiful international ' +
    'optimization algorithms compute hyphenation points',
  hyphenHeavy:
    'international optimization international optimization international beautiful hyphenation',
  longWord: 'mmm mmmm supercalifragilisticexpialidociousness mmm mmmm',
  small: 'The quick brown fox jumps over a lazy dog',
  // Word widths 4,12,4,8,12 with glue 3/2/1 at lineWidth 34:
  // greedy packs line 1 to width 26 (badness 800) and pays 806 total;
  // the optimum shrinks line 1 to 37/34 (badness 100) and pays 150.
  greedyTrap: 'li arii li af ms',
};

/** Fixture with two equal-cost optimal layouts; the earlier breaks must win. */
export const TIE_FIXTURE: { nodes: import('./types.ts').Node[]; options: BreakOptions } = {
  nodes: [
    { kind: 'box', width: 17, text: 'b0' },
    { kind: 'glue', width: 4, stretch: 2, shrink: 3 },
    { kind: 'box', width: 17, text: 'b1' },
    { kind: 'glue', width: 3, stretch: 2, shrink: 3 },
    { kind: 'box', width: 10, text: 'b2' },
    { kind: 'glue', width: 3, stretch: 2, shrink: 3 },
    { kind: 'box', width: 17, text: 'b3' },
    { kind: 'glue', width: 3, stretch: 2, shrink: 2 },
    { kind: 'box', width: 17, text: 'b4' },
  ],
  options: {
    lineWidth: 32,
    widthDiffWeight: 3,
    maxHyphens: 0,
    forbidConsecutiveHyphens: true,
    budget: 100000,
  },
};

/** Options under which PARAGRAPHS.greedyTrap separates greedy from optimal. */
export const GREEDY_TRAP_OPTIONS: BreakOptions = {
  lineWidth: 34,
  widthDiffWeight: 2,
  maxHyphens: 0,
  forbidConsecutiveHyphens: true,
  budget: 1_000_000,
};
