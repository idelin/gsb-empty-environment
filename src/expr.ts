import type { Expr, Value, VarVersion } from './model.ts';
import { varKey } from './model.ts';

export class MissingProducerError extends Error {
  readonly missing: VarVersion;
  constructor(missing: VarVersion) {
    super(`missing producer for ${varKey(missing)}`);
    this.missing = missing;
  }
}

function asNumber(v: Value): number {
  if (typeof v !== 'number') throw new Error(`expected number, got ${JSON.stringify(v)}`);
  return v;
}

function truthy(v: Value): boolean {
  return Boolean(v);
}

export function evalExpr(expr: Expr, env: ReadonlyMap<string, Value>): Value {
  switch (expr.op) {
    case 'lit':
      return expr.value;
    case 'ref': {
      const key = varKey(expr.ref);
      if (!env.has(key)) throw new MissingProducerError(expr.ref);
      return env.get(key)!;
    }
    case 'neg':
      return -asNumber(evalExpr(expr.arg, env));
    case 'not':
      return !truthy(evalExpr(expr.arg, env));
    default: {
      const lhs = evalExpr(expr.lhs, env);
      const rhs = evalExpr(expr.rhs, env);
      switch (expr.op) {
        case 'add': return asNumber(lhs) + asNumber(rhs);
        case 'sub': return asNumber(lhs) - asNumber(rhs);
        case 'mul': return asNumber(lhs) * asNumber(rhs);
        case 'div': return asNumber(lhs) / asNumber(rhs);
        case 'mod': return asNumber(lhs) % asNumber(rhs);
        case 'eq': return lhs === rhs;
        case 'ne': return lhs !== rhs;
        case 'lt': return asNumber(lhs) < asNumber(rhs);
        case 'le': return asNumber(lhs) <= asNumber(rhs);
        case 'gt': return asNumber(lhs) > asNumber(rhs);
        case 'ge': return asNumber(lhs) >= asNumber(rhs);
        case 'and': return truthy(lhs) && truthy(rhs);
        case 'or': return truthy(lhs) || truthy(rhs);
      }
    }
  }
}

export function exprRefs(expr: Expr, out: VarVersion[] = []): VarVersion[] {
  switch (expr.op) {
    case 'lit':
      break;
    case 'ref':
      out.push(expr.ref);
      break;
    case 'neg':
    case 'not':
      exprRefs(expr.arg, out);
      break;
    default:
      exprRefs(expr.lhs, out);
      exprRefs(expr.rhs, out);
  }
  return out;
}
