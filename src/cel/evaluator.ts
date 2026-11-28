import { Node } from './ast.ts';
import { parse, ParseError } from './parser.ts';
import { LexError, decodeString } from './lexer.ts';
import { CelInt, int64Checked, truncDiv } from './int.ts';

/** Error raised while evaluating a CEL expression; maps onto failurePolicy handling. */
export class CelError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CelError';
  }
}

export type CelValue =
  | null
  | boolean
  | string
  | number
  | CelInt
  | CelValue[]
  | { [key: string]: CelValue };

export type CelEnv = Record<string, CelValue>;

interface CompiledExpression {
  ast: Node;
  source: string;
}

const cache = new Map<string, CompiledExpression>();

export function compile(expression: string): CompiledExpression {
  const cached = cache.get(expression);
  if (cached) return cached;
  let ast: Node;
  try {
    ast = parse(expression);
  } catch (err) {
    if (err instanceof ParseError || err instanceof LexError) {
      throw new CelError(`expression compilation failed: ${err.message}`);
    }
    throw err;
  }
  const compiled = { ast, source: expression };
  cache.set(expression, compiled);
  return compiled;
}

/** Evaluate a CEL expression against the given environment (variables). */
export function evaluate(expression: string, env: CelEnv): CelValue {
  const compiled = compile(expression);
  return new Evaluator(env).eval(compiled.ast);
}

export function typeName(value: CelValue): string {
  if (value === null) return 'null';
  if (value instanceof CelInt) return 'int';
  if (Array.isArray(value)) return 'list';
  if (typeof value === 'object') return 'map';
  return typeof value;
}

export function isMap(value: CelValue): value is { [key: string]: CelValue } {
  return value !== null && typeof value === 'object' && !Array.isArray(value) && !(value instanceof CelInt);
}

export function celEquals(a: CelValue, b: CelValue): boolean {
  if (a instanceof CelInt || b instanceof CelInt) {
    return a instanceof CelInt && b instanceof CelInt && a.equals(b);
  }
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((item, i) => celEquals(item, b[i]));
  }
  if (isMap(a) || isMap(b)) {
    if (!isMap(a) || !isMap(b)) return false;
    const ak = Object.keys(a);
    const bk = Object.keys(b);
    if (ak.length !== bk.length) return false;
    return ak.every((k) => k in b && celEquals(a[k], b[k]));
  }
  return a === b;
}

class Evaluator {
  private readonly bindings: Array<Map<string, CelValue>> = [];

  constructor(private readonly env: CelEnv) {}

  eval(node: Node): CelValue {
    switch (node.kind) {
      case 'literal':
        return this.evalLiteral(node);
      case 'ident':
        return this.resolveIdent(node.name);
      case 'select':
        return this.evalSelect(node.operand, node.field);
      case 'index':
        return this.evalIndex(node.operand, node.index);
      case 'call':
        return this.evalCall(node);
      case 'list':
        return node.elements.map((el) => this.eval(el));
      case 'map': {
        const out: Record<string, CelValue> = {};
        for (const entry of node.entries) {
          const key = this.eval(entry.key);
          if (typeof key !== 'string') {
            throw new CelError(`map key must be a string, got ${typeName(key)}`);
          }
          out[key] = this.eval(entry.value);
        }
        return out;
      }
      case 'unary':
        return this.evalUnary(node);
      case 'binary':
        return this.evalBinary(node);
      case 'logical':
        return this.evalLogical(node);
      case 'in':
        return this.evalIn(node.left, node.right);
      case 'conditional': {
        const test = this.eval(node.test);
        if (typeof test !== 'boolean') {
          throw new CelError(`conditional test must be bool, got ${typeName(test)}`);
        }
        return test ? this.eval(node.consequent) : this.eval(node.alternate);
      }
      case 'has':
        return this.evalHas(node.operand);
      case 'macro':
        return this.evalMacro(node);
      default: {
        const exhaustive: never = node;
        throw new CelError(`unsupported node: ${JSON.stringify(exhaustive)}`);
      }
    }
  }

  private evalLiteral(node: { type: string; raw: string }): CelValue {
    switch (node.type) {
      case 'int':
        if (node.raw.startsWith('0x') || node.raw.startsWith('0X')) {
          return new CelInt(BigInt(node.raw));
        }
        return new CelInt(node.raw);
      case 'uint':
        return new CelInt(node.raw);
      case 'double':
        return Number(node.raw);
      case 'string':
      case 'bytes':
        return decodeString(node.raw);
      case 'bool':
        return node.raw === 'true';
      case 'null':
        return null;
      default:
        throw new CelError(`unsupported literal type ${node.type}`);
    }
  }

  private resolveIdent(name: string): CelValue {
    for (let i = this.bindings.length - 1; i >= 0; i -= 1) {
      const scope = this.bindings[i];
      if (scope.has(name)) return scope.get(name) as CelValue;
    }
    if (name in this.env) return this.env[name];
    throw new CelError(`undeclared reference to '${name}'`);
  }

  private evalSelect(operandNode: Node, field: string): CelValue {
    const operand = this.eval(operandNode);
    if (!isMap(operand)) {
      throw new CelError(`cannot select field '${field}' on ${typeName(operand)}`);
    }
    if (!(field in operand)) {
      throw new CelError(`no such key: '${field}'`);
    }
    return operand[field];
  }

  private evalIndex(operandNode: Node, indexNode: Node): CelValue {
    const operand = this.eval(operandNode);
    const index = this.eval(indexNode);
    if (Array.isArray(operand)) {
      if (!(index instanceof CelInt)) {
        throw new CelError(`list index must be int, got ${typeName(index)}`);
      }
      const i = Number(index.value);
      if (i < 0 || i >= operand.length) {
        throw new CelError(`index ${i} out of bounds for list of size ${operand.length}`);
      }
      return operand[i];
    }
    if (isMap(operand)) {
      if (typeof index !== 'string') {
        throw new CelError(`map key must be string, got ${typeName(index)}`);
      }
      if (!(index in operand)) {
        throw new CelError(`no such key: '${index}'`);
      }
      return operand[index];
    }
    throw new CelError(`cannot index value of type ${typeName(operand)}`);
  }

  private evalUnary(node: { op: '!' | '-' | '+'; operand: Node }): CelValue {
    const v = this.eval(node.operand);
    if (node.op === '!') {
      if (typeof v !== 'boolean') {
        throw new CelError(`'!' requires bool, got ${typeName(v)}`);
      }
      return !v;
    }
    if (v instanceof CelInt) {
      if (node.op === '-') return new CelInt(int64Checked(-v.value));
      return v;
    }
    if (typeof v === 'number') {
      if (node.op === '-') return -v;
      return v;
    }
    throw new CelError(`unary '${node.op}' requires int or double, got ${typeName(v)}`);
  }

  private evalBinary(node: {
    op: string;
    left: Node;
    right: Node;
  }): CelValue {
    if (node.op === '==') {
      return celEquals(this.eval(node.left), this.eval(node.right));
    }
    if (node.op === '!=') {
      return !celEquals(this.eval(node.left), this.eval(node.right));
    }

    const l = this.eval(node.left);
    const r = this.eval(node.right);

    switch (node.op) {
      case '+':
        return this.opAdd(l, r);
      case '-':
      case '*':
      case '/':
      case '%':
        return this.opArithmetic(node.op, l, r);
      case '<':
      case '<=':
      case '>':
      case '>=':
        return this.opCompare(node.op, l, r);
      default:
        throw new CelError(`unsupported operator ${node.op}`);
    }
  }

  private opAdd(l: CelValue, r: CelValue): CelValue {
    if (l instanceof CelInt && r instanceof CelInt) {
      return new CelInt(int64Checked(l.value + r.value));
    }
    if (typeof l === 'number' && typeof r === 'number') return l + r;
    if (typeof l === 'string' && typeof r === 'string') return l + r;
    if (Array.isArray(l) && Array.isArray(r)) return [...l, ...r];
    throw new CelError(`no matching overload for '+': (${typeName(l)}, ${typeName(r)})`);
  }

  private opArithmetic(op: string, l: CelValue, r: CelValue): CelValue {
    if (l instanceof CelInt && r instanceof CelInt) {
      switch (op) {
        case '-':
          return new CelInt(int64Checked(l.value - r.value));
        case '*':
          return new CelInt(int64Checked(l.value * r.value));
        case '/':
          return new CelInt(int64Checked(truncDiv(l.value, r.value)));
        case '%':
          if (r.isZero()) throw new CelError('modulo by zero');
          return new CelInt(int64Checked(l.value % r.value));
        default:
          throw new CelError(`unsupported int operator ${op}`);
      }
    }
    if (typeof l === 'number' && typeof r === 'number') {
      switch (op) {
        case '-':
          return l - r;
        case '*':
          return l * r;
        case '/':
          if (r === 0) throw new CelError('division by zero');
          return l / r;
        case '%':
          if (r === 0) throw new CelError('modulo by zero');
          return l % r;
        default:
          throw new CelError(`unsupported double operator ${op}`);
      }
    }
    throw new CelError(
      `no matching overload for '${op}': (${typeName(l)}, ${typeName(r)})`,
    );
  }

  private opCompare(op: string, l: CelValue, r: CelValue): boolean {
    let cmp: number;
    if (l instanceof CelInt && r instanceof CelInt) {
      cmp = l.value < r.value ? -1 : l.value > r.value ? 1 : 0;
    } else if (typeof l === 'number' && typeof r === 'number') {
      cmp = l < r ? -1 : l > r ? 1 : 0;
    } else if (typeof l === 'string' && typeof r === 'string') {
      cmp = l < r ? -1 : l > r ? 1 : 0;
    } else {
      throw new CelError(
        `no matching overload for '${op}': (${typeName(l)}, ${typeName(r)})`,
      );
    }
    switch (op) {
      case '<':
        return cmp < 0;
      case '<=':
        return cmp <= 0;
      case '>':
        return cmp > 0;
      case '>=':
        return cmp >= 0;
      default:
        throw new CelError(`unsupported comparison ${op}`);
    }
  }

  private evalLogical(node: { op: '&&' | '||'; left: Node; right: Node }): boolean {
    const left = this.eval(node.left);
    if (typeof left !== 'boolean') {
      throw new CelError(`'${node.op}' requires bool operands, got ${typeName(left)}`);
    }
    if (node.op === '&&' && !left) return false;
    if (node.op === '||' && left) return true;
    const right = this.eval(node.right);
    if (typeof right !== 'boolean') {
      throw new CelError(`'${node.op}' requires bool operands, got ${typeName(right)}`);
    }
    return right;
  }

  private evalIn(leftNode: Node, rightNode: Node): boolean {
    const left = this.eval(leftNode);
    const right = this.eval(rightNode);
    if (Array.isArray(right)) {
      return right.some((item) => celEquals(item, left));
    }
    if (isMap(right)) {
      if (typeof left !== 'string') {
        throw new CelError(`map membership key must be string, got ${typeName(left)}`);
      }
      return left in right;
    }
    if (typeof right === 'string') {
      if (typeof left !== 'string') {
        throw new CelError(`string membership requires string, got ${typeName(left)}`);
      }
      return right.includes(left);
    }
    throw new CelError(`'in' not supported on ${typeName(right)}`);
  }

  private evalHas(node: Node): boolean {
    if (node.kind === 'select') {
      let operand: CelValue;
      try {
        operand = this.eval(node.operand);
      } catch (err) {
        if (err instanceof CelError && err.message.startsWith('no such key')) return false;
        throw err;
      }
      return isMap(operand) && node.field in operand;
    }
    if (node.kind === 'index') {
      const operand = this.eval(node.operand);
      const key = this.eval(node.index);
      if (!isMap(operand)) return false;
      if (typeof key !== 'string') return false;
      return key in operand;
    }
    throw new CelError('has() requires a field access expression');
  }

  private withBinding<T>(name: string, value: CelValue, fn: () => T): T {
    const scope = new Map<string, CelValue>([[name, value]]);
    this.bindings.push(scope);
    try {
      return fn();
    } finally {
      this.bindings.pop();
    }
  }

  private evalMacro(node: {
    name: string;
    receiver: Node;
    bindVar: string | null;
    predicate: Node | null;
  }): CelValue {
    if (node.name === 'size') {
      return celSize(this.eval(node.receiver));
    }
    const receiver = this.eval(node.receiver);
    const bindVar = node.bindVar;
    const predicate = node.predicate;
    if (bindVar === null || predicate === null) {
      throw new CelError(`macro ${node.name} requires a bound variable and predicate`);
    }
    if (!Array.isArray(receiver)) {
      throw new CelError(`macro ${node.name} requires a list, got ${typeName(receiver)}`);
    }

    const truthy = (item: CelValue): boolean => {
      const result = this.withBinding(bindVar, item, () => this.eval(predicate));
      if (typeof result !== 'boolean') {
        throw new CelError(`macro ${node.name} predicate must return bool`);
      }
      return result;
    };

    switch (node.name) {
      case 'all':
        return receiver.every(truthy);
      case 'exists':
        return receiver.some(truthy);
      case 'exists_one':
        return receiver.filter(truthy).length === 1;
      case 'filter':
        return receiver.filter(truthy);
      case 'map':
        return receiver.map((item) =>
          this.withBinding(bindVar, item, () => this.eval(predicate)),
        );
      default:
        throw new CelError(`unsupported macro ${node.name}`);
    }
  }

  private evalCall(node: {
    receiver: Node | null;
    name: string;
    args: Node[];
  }): CelValue {
    const receiver = node.receiver === null ? null : this.eval(node.receiver);
    const args = node.args.map((arg) => this.eval(arg));
    return dispatchFunction(node.name, receiver, args);
  }
}

function celSize(value: CelValue): CelInt {
  if (typeof value === 'string') return new CelInt(value.length);
  if (Array.isArray(value)) return new CelInt(value.length);
  if (isMap(value)) return new CelInt(Object.keys(value).length);
  throw new CelError(`size() not supported on ${typeName(value)}`);
}

function requireArgs(name: string, args: CelValue[], count: number): void {
  if (args.length !== count) {
    throw new CelError(`${name}() expects ${count} argument(s), got ${args.length}`);
  }
}

function toInt(value: CelValue): CelInt {
  if (value instanceof CelInt) return value;
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new CelError(`int() conversion error: ${value}`);
    return new CelInt(Math.trunc(value));
  }
  if (typeof value === 'string') {
    try {
      return new CelInt(value.trim());
    } catch {
      throw new CelError(`int() conversion error for ${JSON.stringify(value)}`);
    }
  }
  if (typeof value === 'boolean') return new CelInt(value ? 1 : 0);
  throw new CelError(`int() not supported for ${typeName(value)}`);
}

function toDouble(value: CelValue): number {
  if (typeof value === 'number') return value;
  if (value instanceof CelInt) return Number(value.value);
  if (typeof value === 'string') {
    const n = Number(value.trim());
    if (Number.isNaN(n)) throw new CelError(`double() conversion error for ${JSON.stringify(value)}`);
    return n;
  }
  throw new CelError(`double() not supported for ${typeName(value)}`);
}

function toCelString(value: CelValue): string {
  if (typeof value === 'string') return value;
  if (value instanceof CelInt) return value.toString();
  if (typeof value === 'number') return String(value);
  if (typeof value === 'boolean') return String(value);
  if (value === null) return 'null';
  return JSON.stringify(value);
}

function toBool(value: CelValue): boolean {
  if (typeof value === 'boolean') return value;
  throw new CelError(`bool() not supported for ${typeName(value)}`);
}

function dispatchFunction(
  name: string,
  receiver: CelValue,
  args: CelValue[],
): CelValue {
  // Global functions (receiver === null).
  if (receiver === null) {
    switch (name) {
      case 'size':
        requireArgs(name, args, 1);
        return celSize(args[0]);
      case 'int':
        requireArgs(name, args, 1);
        return toInt(args[0]);
      case 'uint':
        requireArgs(name, args, 1);
        return toInt(args[0]);
      case 'double':
        requireArgs(name, args, 1);
        return toDouble(args[0]);
      case 'string':
        requireArgs(name, args, 1);
        return toCelString(args[0]);
      case 'bool':
        requireArgs(name, args, 1);
        return toBool(args[0]);
      case 'type':
        requireArgs(name, args, 1);
        return typeName(args[0]);
      case 'timestamp':
        throw new CelError('timestamp() is not supported by this CEL subset');
      case 'duration':
        throw new CelError('duration() is not supported by this CEL subset');
      default:
        throw new CelError(`unknown function ${name}()`);
    }
  }

  switch (name) {
    case 'size':
      requireArgs(name, args, 0);
      return celSize(receiver);
    case 'string':
      requireArgs(name, args, 0);
      return toCelString(receiver);
    case 'contains': {
      requireArgs(name, args, 1);
      if (typeof receiver !== 'string' || typeof args[0] !== 'string') {
        throw new CelError('contains() requires two strings');
      }
      return receiver.includes(args[0]);
    }
    case 'startsWith': {
      requireArgs(name, args, 1);
      if (typeof receiver !== 'string' || typeof args[0] !== 'string') {
        throw new CelError('startsWith() requires two strings');
      }
      return receiver.startsWith(args[0]);
    }
    case 'endsWith': {
      requireArgs(name, args, 1);
      if (typeof receiver !== 'string' || typeof args[0] !== 'string') {
        throw new CelError('endsWith() requires two strings');
      }
      return receiver.endsWith(args[0]);
    }
    case 'matches': {
      requireArgs(name, args, 1);
      if (typeof receiver !== 'string' || typeof args[0] !== 'string') {
        throw new CelError('matches() requires two strings');
      }
      try {
        return new RegExp(args[0]).test(receiver);
      } catch (err) {
        throw new CelError(`invalid regex ${JSON.stringify(args[0])}: ${(err as Error).message}`);
      }
    }
    case 'lowerAscii':
      requireArgs(name, args, 0);
      if (typeof receiver !== 'string') throw new CelError('lowerAscii() requires string');
      return receiver.toLowerCase();
    case 'upperAscii':
      requireArgs(name, args, 0);
      if (typeof receiver !== 'string') throw new CelError('upperAscii() requires string');
      return receiver.toUpperCase();
    case 'trim':
      requireArgs(name, args, 0);
      if (typeof receiver !== 'string') throw new CelError('trim() requires string');
      return receiver.trim();
    case 'split': {
      requireArgs(name, args, 1);
      if (typeof receiver !== 'string' || typeof args[0] !== 'string') {
        throw new CelError('split() requires two strings');
      }
      return receiver.split(args[0]);
    }
    case 'replace': {
      requireArgs(name, args, 2);
      if (
        typeof receiver !== 'string' ||
        typeof args[0] !== 'string' ||
        typeof args[1] !== 'string'
      ) {
        throw new CelError('replace() requires three strings');
      }
      return receiver.split(args[0]).join(args[1]);
    }
    case 'indexOf': {
      requireArgs(name, args, 1);
      if (typeof receiver === 'string' && typeof args[0] === 'string') {
        return new CelInt(receiver.indexOf(args[0]));
      }
      if (Array.isArray(receiver)) {
        const idx = receiver.findIndex((item) => celEquals(item, args[0]));
        return new CelInt(idx);
      }
      throw new CelError('indexOf() requires a string or list receiver');
    }
    case 'join': {
      if (!Array.isArray(receiver)) throw new CelError('join() requires a list');
      const sep = args.length === 0 ? '' : args[0];
      if (typeof sep !== 'string') throw new CelError('join() separator must be a string');
      return receiver.map(toCelString).join(sep);
    }
    default:
      throw new CelError(`unknown function ${name}()`);
  }
}
