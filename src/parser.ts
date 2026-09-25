import { PathError } from './errors.ts';
import type {
  BoolExpr,
  CmpOp,
  Literal,
  Operand,
  PathAst,
  RelStep,
  Segment,
} from './ast.ts';

/** Hard limits enforced at parse time. */
export const LIMITS = {
  maxSourceLength: 512,
  maxSegments: 32,
  maxFilterDepth: 8,
  maxSetSize: 64,
  maxArrayIndex: 0xffffffff,
} as const;

/** Keys that are never readable, regardless of own-property status. */
export const FORBIDDEN_KEYS: ReadonlySet<string> = new Set([
  '__proto__',
  'constructor',
  'prototype',
]);

type Token =
  | { t: 'dot' }
  | { t: 'dotdot' }
  | { t: 'lbracket' }
  | { t: 'rbracket' }
  | { t: 'lparen' }
  | { t: 'rparen' }
  | { t: 'lbrace' }
  | { t: 'rbrace' }
  | { t: 'comma' }
  | { t: 'question' }
  | { t: 'at' }
  | { t: 'bang' }
  | { t: 'and' }
  | { t: 'or' }
  | { t: 'star' }
  | { t: 'cmp'; op: CmpOp }
  | { t: 'in' }
  | { t: 'ident'; name: string }
  | { t: 'int'; value: number }
  | { t: 'str'; value: string }
  | { t: 'eof' };

const IDENT_START = /[A-Za-z_$]/;
const IDENT_PART = /[A-Za-z0-9_$]/;
const DIGIT = /[0-9]/;

function lex(source: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  const fail = (detail: string): never => {
    throw new PathError('PARSE_ERROR', source, `${detail} at offset ${i}`);
  };
  while (i < source.length) {
    const c = source[i]!;
    if (c === ' ' || c === '\t' || c === '\n' || c === '\r') {
      i++;
      continue;
    }
    if (c === '.') {
      if (source[i + 1] === '.') {
        tokens.push({ t: 'dotdot' });
        i += 2;
      } else {
        tokens.push({ t: 'dot' });
        i += 1;
      }
      continue;
    }
    if (c === '[') { tokens.push({ t: 'lbracket' }); i++; continue; }
    if (c === ']') { tokens.push({ t: 'rbracket' }); i++; continue; }
    if (c === '(') { tokens.push({ t: 'lparen' }); i++; continue; }
    if (c === ')') { tokens.push({ t: 'rparen' }); i++; continue; }
    if (c === '{') { tokens.push({ t: 'lbrace' }); i++; continue; }
    if (c === '}') { tokens.push({ t: 'rbrace' }); i++; continue; }
    if (c === ',') { tokens.push({ t: 'comma' }); i++; continue; }
    if (c === '?') { tokens.push({ t: 'question' }); i++; continue; }
    if (c === '@') { tokens.push({ t: 'at' }); i++; continue; }
    if (c === '*') { tokens.push({ t: 'star' }); i++; continue; }
    if (c === '!') {
      if (source[i + 1] === '=') { tokens.push({ t: 'cmp', op: '!=' }); i += 2; }
      else { tokens.push({ t: 'bang' }); i++; }
      continue;
    }
    if (c === '=') {
      if (source[i + 1] === '=') { tokens.push({ t: 'cmp', op: '==' }); i += 2; continue; }
      fail(`unexpected '=' (use '==')`);
    }
    if (c === '<') {
      if (source[i + 1] === '=') { tokens.push({ t: 'cmp', op: '<=' }); i += 2; }
      else { tokens.push({ t: 'cmp', op: '<' }); i++; }
      continue;
    }
    if (c === '>') {
      if (source[i + 1] === '=') { tokens.push({ t: 'cmp', op: '>=' }); i += 2; }
      else { tokens.push({ t: 'cmp', op: '>' }); i++; }
      continue;
    }
    if (c === '&') {
      if (source[i + 1] === '&') { tokens.push({ t: 'and' }); i += 2; continue; }
      fail(`unexpected '&' (use '&&')`);
    }
    if (c === '|') {
      if (source[i + 1] === '|') { tokens.push({ t: 'or' }); i += 2; continue; }
      fail(`unexpected '|' (use '||')`);
    }
    if (c === "'" || c === '"') {
      const quote = c;
      i++;
      let out = '';
      for (;;) {
        if (i >= source.length) fail('unterminated string literal');
        const ch = source[i]!;
        if (ch === quote) { i++; break; }
        if (ch === '\\') {
          const esc = source[i + 1];
          if (esc === undefined) fail('unterminated escape');
          const map: Record<string, string> = {
            n: '\n', t: '\t', r: '\r', '\\': '\\', "'": "'", '"': '"',
          };
          const rep = map[esc];
          if (rep === undefined) fail(`unsupported escape '\\${esc}'`);
          out += rep;
          i += 2;
          continue;
        }
        out += ch;
        i++;
      }
      tokens.push({ t: 'str', value: out });
      continue;
    }
    if (DIGIT.test(c) || (c === '-' && DIGIT.test(source[i + 1] ?? ''))) {
      let j = i;
      if (source[j] === '-') j++;
      while (j < source.length && DIGIT.test(source[j]!)) j++;
      if (source[j] === '.' || source[j] === 'e' || source[j] === 'E') {
        fail('only integer literals are allowed');
      }
      const raw = source.slice(i, j);
      const value = Number.parseInt(raw, 10);
      if (!Number.isSafeInteger(value)) fail('integer literal out of range');
      tokens.push({ t: 'int', value });
      i = j;
      continue;
    }
    if (IDENT_START.test(c)) {
      let j = i + 1;
      while (j < source.length && IDENT_PART.test(source[j]!)) j++;
      const word = source.slice(i, j);
      if (word === 'in') tokens.push({ t: 'in' });
      else tokens.push({ t: 'ident', name: word });
      i = j;
      continue;
    }
    fail(`unexpected character ${JSON.stringify(c)}`);
  }
  tokens.push({ t: 'eof' });
  return tokens;
}

class Parser {
  private pos = 0;
  private readonly tokens: Token[];
  private readonly source: string;

  constructor(tokens: Token[], source: string) {
    this.tokens = tokens;
    this.source = source;
  }

  private peek(): Token {
    return this.tokens[this.pos]!;
  }

  private next(): Token {
    return this.tokens[this.pos++]!;
  }

  private fail(detail: string): never {
    throw new PathError('PARSE_ERROR', this.source, detail);
  }

  private expect<T extends Token['t']>(t: T, what: string): Token {
    const tok = this.next();
    if (tok.t !== t) this.fail(`expected ${what}`);
    return tok;
  }

  private checkKey(name: string): string {
    if (FORBIDDEN_KEYS.has(name)) {
      throw new PathError(
        'FORBIDDEN_PROPERTY',
        this.source,
        `access to property ${JSON.stringify(name)} is denied`,
      );
    }
    return name;
  }

  parsePath(): PathAst {
    const segments: Segment[] = [];
    // A leading bare identifier is a key segment: "a.b" == "a" then ".b".
    if (this.peek().t === 'ident') {
      const name = (this.next() as { name: string }).name;
      segments.push({ kind: 'key', name: this.checkKey(name) });
    }
    // A leading "*" is a root-level wildcard.
    if (this.peek().t === 'star') {
      this.next();
      segments.push({ kind: 'wildcard' });
    }
    for (;;) {
      const tok = this.peek();
      if (tok.t === 'eof') break;
      if (segments.length >= LIMITS.maxSegments) {
        throw new PathError(
          'DEPTH_LIMIT',
          this.source,
          `more than ${LIMITS.maxSegments} path segments`,
        );
      }
      segments.push(this.parseSegment());
    }
    if (segments.length === 0) this.fail('empty path expression');
    return { source: this.source, segments };
  }

  private parseSegment(): Segment {
    const tok = this.next();
    switch (tok.t) {
      case 'dot': {
        if (this.peek().t === 'star') {
          this.next();
          return { kind: 'wildcard' };
        }
        const name = this.expect('ident', 'property name after "."');
        return { kind: 'key', name: this.checkKey((name as { name: string }).name) };
      }
      case 'dotdot': {
        const name = this.expect('ident', 'property name after ".."');
        return { kind: 'descend', name: this.checkKey((name as { name: string }).name) };
      }
      case 'lbracket':
        return this.parseBracket();
      default:
        this.fail('expected ".", ".." or "[" to start a segment');
    }
  }

  private parseBracket(): Segment {
    const tok = this.next();
    if (tok.t === 'star') {
      this.expect('rbracket', '"]" after "*"');
      return { kind: 'wildcard' };
    }
    if (tok.t === 'int') {
      const index = (tok as { value: number }).value;
      if (index < 0 || index > LIMITS.maxArrayIndex) {
        this.fail('array index out of range');
      }
      this.expect('rbracket', '"]" after index');
      return { kind: 'index', index };
    }
    if (tok.t === 'str') {
      const name = this.checkKey((tok as { value: string }).value);
      this.expect('rbracket', '"]" after string');
      return { kind: 'key', name };
    }
    if (tok.t === 'question') {
      this.expect('lparen', '"(" after "[?"');
      const expr = this.parseOr(1);
      this.expect('rparen', '")" after filter expression');
      this.expect('rbracket', '"]" after filter');
      return { kind: 'filter', expr };
    }
    this.fail('expected "*", integer, string or "?(" inside brackets');
  }

  private parseOr(depth: number): BoolExpr {
    if (depth > LIMITS.maxFilterDepth) {
      throw new PathError(
        'DEPTH_LIMIT',
        this.source,
        `filter nesting deeper than ${LIMITS.maxFilterDepth}`,
      );
    }
    const items: BoolExpr[] = [this.parseAnd(depth)];
    while (this.peek().t === 'or') {
      this.next();
      items.push(this.parseAnd(depth));
    }
    return items.length === 1 ? items[0]! : { kind: 'or', items };
  }

  private parseAnd(depth: number): BoolExpr {
    const items: BoolExpr[] = [this.parseNot(depth)];
    while (this.peek().t === 'and') {
      this.next();
      items.push(this.parseNot(depth));
    }
    return items.length === 1 ? items[0]! : { kind: 'and', items };
  }

  private parseNot(depth: number): BoolExpr {
    if (this.peek().t === 'bang') {
      this.next();
      return { kind: 'not', item: this.parseNot(depth) };
    }
    return this.parsePrimary(depth);
  }

  private parsePrimary(depth: number): BoolExpr {
    if (this.peek().t === 'lparen') {
      this.next();
      const inner = this.parseOr(depth + 1);
      this.expect('rparen', '")"');
      return inner;
    }
    const left = this.parseOperand();
    const tok = this.next();
    if (tok.t === 'cmp') {
      const right = this.parseOperand();
      return { kind: 'cmp', op: (tok as { op: CmpOp }).op, left, right };
    }
    if (tok.t === 'in') {
      return { kind: 'in', target: left, set: this.parseSet() };
    }
    this.fail('expected comparison operator or "in" in filter');
  }

  private parseSet(): Literal[] {
    this.expect('lbrace', '"{" to start a set');
    const items: Literal[] = [];
    for (;;) {
      items.push(this.parseLiteral());
      if (items.length > LIMITS.maxSetSize) {
        throw new PathError(
          'DEPTH_LIMIT',
          this.source,
          `set literal larger than ${LIMITS.maxSetSize} entries`,
        );
      }
      const tok = this.next();
      if (tok.t === 'comma') continue;
      if (tok.t === 'rbrace') break;
      this.fail('expected "," or "}" in set literal');
    }
    if (items.length === 0) this.fail('set literal must not be empty');
    return items;
  }

  private parseLiteral(): Literal {
    const tok = this.next();
    if (tok.t === 'int') return (tok as { value: number }).value;
    if (tok.t === 'str') return (tok as { value: string }).value;
    if (tok.t === 'ident') {
      const word = (tok as { name: string }).name;
      if (word === 'true') return true;
      if (word === 'false') return false;
      if (word === 'null') return null;
    }
    this.fail('expected integer, string, true, false or null');
  }

  private parseOperand(): Operand {
    const tok = this.peek();
    if (tok.t === 'at') {
      this.next();
      const steps: RelStep[] = [];
      for (;;) {
        const t = this.next();
        if (t.t === 'dot') {
          const name = this.expect('ident', 'property name after "@."');
          steps.push({ kind: 'key', name: this.checkKey((name as { name: string }).name) });
          continue;
        }
        if (t.t === 'lbracket') {
          const inner = this.next();
          if (inner.t === 'int') {
            const index = (inner as { value: number }).value;
            if (index < 0) this.fail('array index out of range');
            steps.push({ kind: 'index', index });
          } else if (inner.t === 'str') {
            steps.push({
              kind: 'key',
              name: this.checkKey((inner as { value: string }).value),
            });
          } else {
            this.fail('expected integer or string inside "@[]"');
          }
          this.expect('rbracket', '"]"');
          continue;
        }
        this.pos--;
        break;
      }
      return { kind: 'self', steps };
    }
    return { kind: 'literal', value: this.parseLiteral() };
  }
}

/**
 * Parse a restricted path expression. All grammar, depth and policy
 * violations are reported here, before any data is touched.
 */
export function parsePath(source: string): PathAst {
  if (typeof source !== 'string' || source.length === 0) {
    throw new PathError('PARSE_ERROR', String(source), 'path must be a non-empty string');
  }
  if (source.length > LIMITS.maxSourceLength) {
    throw new PathError(
      'DEPTH_LIMIT',
      source.slice(0, 64),
      `path longer than ${LIMITS.maxSourceLength} characters`,
    );
  }
  const parser = new Parser(lex(source), source);
  const ast = parser.parsePath();
  return ast;
}
