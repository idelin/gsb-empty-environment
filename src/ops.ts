import type { Value } from './types.js';

function toNum(v: Value): number {
  if (typeof v === 'number') {
    return v;
  }
  throw new Error('numeric operator received non-number: ' + String(v));
}

function toBool(v: Value): boolean {
  if (typeof v === 'boolean') {
    return v;
  }
  throw new Error('boolean operator received non-boolean: ' + String(v));
}

export const OPS: Record<string, (args: Value[]) => Value> = {
  identity: (args: Value[]): Value => args[0],
  add: (args: Value[]): Value => toNum(args[0]) + toNum(args[1]),
  sub: (args: Value[]): Value => toNum(args[0]) - toNum(args[1]),
  mul: (args: Value[]): Value => toNum(args[0]) * toNum(args[1]),
  div: (args: Value[]): Value => toNum(args[0]) / toNum(args[1]),
  mod: (args: Value[]): Value => toNum(args[0]) % toNum(args[1]),
  neg: (args: Value[]): Value => -toNum(args[0]),
  not: (args: Value[]): Value => !toBool(args[0]),
  and: (args: Value[]): Value => toBool(args[0]) && toBool(args[1]),
  or: (args: Value[]): Value => toBool(args[0]) || toBool(args[1]),
  eq: (args: Value[]): Value => args[0] === args[1],
  neq: (args: Value[]): Value => args[0] !== args[1],
  lt: (args: Value[]): Value => toNum(args[0]) < toNum(args[1]),
  lte: (args: Value[]): Value => toNum(args[0]) <= toNum(args[1]),
  gt: (args: Value[]): Value => toNum(args[0]) > toNum(args[1]),
  gte: (args: Value[]): Value => toNum(args[0]) >= toNum(args[1]),
  concat: (args: Value[]): Value => String(args[0]) + String(args[1]),
};

export function applyOp(op: string, args: Value[]): Value {
  const impl = OPS[op];
  if (impl === undefined) {
    throw new Error('unknown op: ' + op);
  }
  return impl(args);
}
