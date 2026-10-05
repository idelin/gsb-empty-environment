/**
 * Expression grammar: integers, calls, arrays and the four arithmetic operators.
 *
 *   expr     -> term exprRest
 *   exprRest -> '+' term exprRest | '-' term exprRest | eps
 *   term     -> factor termRest
 *   termRest -> '*' factor termRest | '/' factor termRest | eps
 *   factor   -> int | ident '(' args ')' | '[' elems ']' | '(' expr ')'
 *   args     -> expr argsRest | eps
 *   argsRest -> ',' expr argsRest | eps
 *   elems    -> expr elemsRest | eps
 *   elemsRest-> ',' expr elemsRest | eps
 *
 * Written without left recursion and without epsilon cycles so it can be
 * simulated directly as a pushdown automaton.
 */

import type { TokenKind } from './tokens.ts';

export type NonTerminal =
  | 'expr' | 'exprRest'
  | 'term' |'termRest' | 'factor'
  | 'args' | 'argsRest'
  | 'elems' | 'elemsRest';

export type GrammarSymbol = TokenKind | NonTerminal;

export const START: NonTerminal = 'expr';

export const PRODUCTIONS: Record<NonTerminal, GrammarSymbol[][]> = {
  expr:      [['term', 'exprRest']],
  exprRest:  [['+', 'term', 'exprRest'], ['-', 'term', 'exprRest'], []],
  term:      [['factor', 'termRest']],
  termRest:  [['*', 'factor', 'termRest'], ['/', 'factor', 'termRest'], []],
  factor:    [['int'], ['ident', '(', 'args', ')'], ['[', 'elems', ']'], ['(', 'expr', ')']],
  args:      [['expr', 'argsRest'], []],
  argsRest:  [[',', 'expr', 'argsRest'], []],
  elems:     [['expr', 'elemsRest'], []],
  elemsRest: [[',', 'expr', 'elemsRest'], []],
};

const NONTERMINALS = new Set<string>(Object.keys(PRODUCTIONS));

export function isNonTerminal(sym: GrammarSymbol): sym is NonTerminal {
  return NONTERMINALS.has(sym);
}

/** Minimum number of terminals any sentence derived from the symbol contains. */
const MIN_YIELD: Record<NonTerminal, number> = {
  expr: 1, exprRest: 0,
  term: 1, termRest: 0,
  factor: 1,
  args: 0, argsRest: 0,
  elems: 0, elemsRest: 0,
};

export function minYieldOf(sym: GrammarSymbol): number {
  return isNonTerminal(sym) ? MIN_YIELD[sym] : 1;
}
