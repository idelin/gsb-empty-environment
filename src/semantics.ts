// Shared primitive semantics used by both the reference interpreter and the
// transformed-code runtime, so the two cannot drift apart on operators.

import { BinOp } from './ast';

export type Prim = number | boolean | null;

export function truthy(v: unknown): boolean {
  if (typeof v !== 'boolean') {
    throw new Error(`condition must be boolean, got ${typeof v}`);
  }
  return v;
}

function expectNumbers(op: BinOp, l: unknown, r: unknown): [number, number] {
  if (typeof l !== 'number' || typeof r !== 'number') {
    throw new Error(`operator ${op} expects numbers`);
  }
  return [l, r];
}

export function evalBinOp(op: BinOp, l: unknown, r: unknown): Prim {
  switch (op) {
    case '+': { const [a, b] = expectNumbers(op, l, r); return a + b; }
    case '-': { const [a, b] = expectNumbers(op, l, r); return a - b; }
    case '*': { const [a, b] = expectNumbers(op, l, r); return a * b; }
    case '%': { const [a, b] = expectNumbers(op, l, r); return a % b; }
    case '<': { const [a, b] = expectNumbers(op, l, r); return a < b; }
    case '<=': { const [a, b] = expectNumbers(op, l, r); return a <= b; }
    case '>': { const [a, b] = expectNumbers(op, l, r); return a > b; }
    case '>=': { const [a, b] = expectNumbers(op, l, r); return a >= b; }
    case '==': return l === r;
    case '!=': return l !== r;
    case 'and': return truthy(l) && truthy(r);
    case 'or': return truthy(l) || truthy(r);
  }
}

export function formatValue(v: unknown): string {
  if (v === null) return 'null';
  if (typeof v === 'number' || typeof v === 'boolean') return String(v);
  if (Array.isArray(v)) return '[' + v.map(formatValue).join(', ') + ']';
  return '<fn>';
}

// Structural snapshot used to compare interpreter results against runtime
// results: closures have no portable identity, so they normalize to '<fn>'.
export function normalize(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(normalize);
  if (typeof v === 'object' && v !== null) return '<fn>';
  return v;
}
