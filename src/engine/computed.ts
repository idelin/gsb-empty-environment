// Shared computed-style data model and cascade ordering helpers.

import type { ValueToken } from '../css/values.ts';
import type { Specificity } from '../css/selector.ts';

export interface PropertyMeta {
  inherited: boolean;
  initial: string;
}

// Inheritance and initial values are kept strictly separate: a property either
// inherits the parent's computed value or falls back to its own initial value.
export const REGULAR_PROPERTIES: Record<string, PropertyMeta> = {
  color: { inherited: true, initial: 'black' },
  'font-family': { inherited: true, initial: 'sans-serif' },
  'font-size': { inherited: true, initial: '16' },
  'font-weight': { inherited: true, initial: '400' },
  'line-height': { inherited: true, initial: '1.2' },
  'text-align': { inherited: true, initial: 'left' },
  visibility: { inherited: true, initial: 'visible' },
  display: { inherited: false, initial: 'block' },
  width: { inherited: false, initial: 'auto' },
  margin: { inherited: false, initial: '0' },
  padding: { inherited: false, initial: '0' },
  background: { inherited: false, initial: 'transparent' },
};

export function isCustomProperty(prop: string): boolean {
  return prop.startsWith('--');
}

export interface Declaration {
  ruleId: number; // stylesheet source order; -1 for inline declarations
  inline: boolean;
  selectorText: string;
  specificity: Specificity;
  prop: string;
  raw: string;
  tokens: ValueToken[];
}

// Cascade order: inline > specificity > source order. A later declaration with
// lower specificity never overrides an earlier one with higher specificity.
export function compareDeclarations(a: Declaration, b: Declaration): number {
  if (a.inline !== b.inline) return a.inline ? 1 : -1;
  for (let k = 0; k < 3; k++) {
    if (a.specificity[k] !== b.specificity[k]) return a.specificity[k] - b.specificity[k];
  }
  return a.ruleId - b.ruleId;
}

export type ValueSource =
  | { kind: 'declaration'; ruleId: number; inline: boolean; selectorText: string; raw: string }
  | { kind: 'inherited'; from: number }
  | { kind: 'initial' }
  | { kind: 'invalid'; unsetTo: 'inherited' | 'initial'; from?: number };

export type ChainOutcome = 'won' | 'lost-specificity' | 'lost-order' | 'invalid-at-computed-value-time';

export interface ChainEntry {
  ruleId: number;
  inline: boolean;
  selectorText: string;
  specificity: Specificity;
  raw: string;
  outcome: ChainOutcome;
}

export interface VarResolution {
  name: string;
  status: 'resolved' | 'fallback' | 'invalid';
  value?: string;
  reason?: 'undefined' | 'cycle' | 'invalid';
  cyclePath?: string[];
}

export interface ComputedValue {
  prop: string;
  value: string | null; // null = guaranteed-invalid (custom properties only)
  source: ValueSource;
  chain: ChainEntry[];
  vars: VarResolution[];
  invalidReason?: 'undefined' | 'cycle' | 'invalid';
  cyclePath?: string[];
}

export function makeKey(nodeId: number, prop: string): string {
  return `${nodeId}|${prop}`;
}

export function splitKey(key: string): [number, string] {
  const bar = key.indexOf('|');
  return [Number(key.slice(0, bar)), key.slice(bar + 1)];
}

export function declarationSource(d: Declaration): ValueSource {
  return { kind: 'declaration', ruleId: d.ruleId, inline: d.inline, selectorText: d.selectorText, raw: d.raw };
}

// Builds the per-property source chain: every candidate declaration in cascade
// order with the reason it won or lost.
export function buildChain(sortedDecls: Declaration[], winnerIacvt: boolean): ChainEntry[] {
  const winner = sortedDecls[sortedDecls.length - 1];
  return sortedDecls.map((d, i) => {
    const isWinner = i === sortedDecls.length - 1;
    let outcome: ChainOutcome;
    if (isWinner) {
      outcome = winnerIacvt ? 'invalid-at-computed-value-time' : 'won';
    } else {
      const lowerTier =
        d.inline !== winner.inline ||
        d.specificity[0] !== winner.specificity[0] ||
        d.specificity[1] !== winner.specificity[1] ||
        d.specificity[2] !== winner.specificity[2];
      outcome = lowerTier ? 'lost-specificity' : 'lost-order';
    }
    return {
      ruleId: d.ruleId,
      inline: d.inline,
      selectorText: d.selectorText,
      specificity: d.specificity,
      raw: d.raw,
      outcome,
    };
  });
}
