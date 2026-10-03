// Recursive-descent / Pratt parser: source text -> Expr AST.
// Unary +/- on a numeric literal folds into a signed number literal, so the
// canonical AST for `-5` is { kind: 'num', value: -5 }.

import { BINARY_PREC, TERNARY_PREC, UNARY_PREC } from './ast.ts';
import type { Expr, Prop } from './ast.ts';

type Tok =
  | { t: 'num'; v: number }
  | { t: 'str'; v: string }
  | { t: 'id'; v: string }
  | { t: 'p'; v: string };

// Longest first so `startsWith` matching is unambiguous.
const PUNCTS = [
  '===', '!==', '>>>',
  '<<', '>>', '<=', '>=', '==', '!=', '&&', '||', '**',
  '+', '-', '*', '/', '%', '<', '>', '&', '|', '^', '!', '~',
  '(', ')', '[', ']', '{', '}', ',', '.', '?', ':',
];

const WORD_UNARY = new Set(['typeof', 'void']);
const WORD_BINARY = new Set(['in', 'instanceof']);

export function tokenize(src: string): Tok[] {
  const toks: Tok[] = [];
  let i = 0;
  const fail = (msg: string): never => {
    throw new SyntaxError(`${msg} (at offset ${i})`);
  };
  while (i < src.length) {
    const ch = src[i];
    if (ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') {
      i++;
      continue;
    }
    if (ch === '/' && src[i + 1] === '/') {
      while (i < src.length && src[i] !== '\n') i++;
      continue;
    }
    if (ch >= '0' && ch <= '9') {
      const m = /^\d+(\.\d+)?([eE][+-]?\d+)?/.exec(src.slice(i))!;
      toks.push({ t: 'num', v: Number(m[0]) });
      i += m[0].length;
      continue;
    }
    if (/[A-Za-z_$]/.test(ch)) {
      const m = /^[A-Za-z_$][A-Za-z0-9_$]*/.exec(src.slice(i))!;
      toks.push({ t: 'id', v: m[0] });
      i += m[0].length;
      continue;
    }
    if (ch === '"') {
      let j = i + 1;
      let s = '';
      while (j < src.length && src[j] !== '"') {
        if (src[j] === '\\') {
          const e = src[j + 1];
          if (e === 'n') s += '\n';
          else if (e === 't') s += '\t';
          else if (e === 'r') s += '\r';
          else if (e === '\\') s += '\\';
          else if (e === '"') s += '"';
          else fail(`unsupported escape \\${e}`);
          j += 2;
        } else {
          s += src[j];
          j++;
        }
      }
      if (j >= src.length) fail('unterminated string literal');
      toks.push({ t: 'str', v: s });
      i = j + 1;
      continue;
    }
    const p = PUNCTS.find((cand) => src.startsWith(cand, i));
    if (p) {
      toks.push({ t: 'p', v: p });
      i += p.length;
      continue;
    }
    fail(`unexpected character '${ch}'`);
  }
  return toks;
}

class Parser {
  private pos = 0;
  private toks: Tok[];

  constructor(toks: Tok[]) {
    this.toks = toks;
  }

  private peek(): Tok | undefined {
    return this.toks[this.pos];
  }

  private next(): Tok {
    const t = this.toks[this.pos++];
    if (!t) throw new SyntaxError('unexpected end of input');
    return t;
  }

  private expectPunct(v: string): void {
    const t = this.next();
    if (t.t !== 'p' || t.v !== v) {
      throw new SyntaxError(`expected '${v}' but got ${JSON.stringify(t)}`);
    }
  }

  parseProgram(): Expr {
    const e = this.parseExpr(0);
    if (this.pos !== this.toks.length) {
      throw new SyntaxError(`unexpected trailing token ${JSON.stringify(this.toks[this.pos])}`);
    }
    return e;
  }

  parseExpr(minPrec: number): Expr {
    let left = this.parsePrefix();
    for (;;) {
      const t = this.peek();
      if (!t) break;
      if (t.t === 'p' && t.v === '(') {
        this.next();
        const args: Expr[] = [];
        if (!(this.peek()?.t === 'p' && (this.peek() as { v: string }).v === ')')) {
          for (;;) {
            args.push(this.parseExpr(0));
            const sep = this.next();
            if (sep.t === 'p' && sep.v === ',') {
              if (this.peek()?.t === 'p' && (this.peek() as { v: string }).v === ')') {
                this.next();
                break;
              }
              continue;
            }
            if (sep.t === 'p' && sep.v === ')') break;
            throw new SyntaxError(`expected ',' or ')' but got ${JSON.stringify(sep)}`);
          }
        } else {
          this.next();
        }
        left = { kind: 'call', callee: left, args };
        continue;
      }
      if (t.t === 'p' && t.v === '.') {
        this.next();
        const name = this.next();
        if (name.t !== 'id') throw new SyntaxError(`expected property name, got ${JSON.stringify(name)}`);
        left = { kind: 'member', object: left, property: name.v };
        continue;
      }
      if (t.t === 'p' && t.v === '[') {
        this.next();
        const index = this.parseExpr(0);
        this.expectPunct(']');
        left = { kind: 'index', object: left, index };
        continue;
      }
      if (t.t === 'p' && t.v === '?') {
        if (TERNARY_PREC < minPrec) break;
        this.next();
        const consequent = this.parseExpr(0);
        this.expectPunct(':');
        const alternate = this.parseExpr(TERNARY_PREC);
        left = { kind: 'cond', test: left, consequent, alternate };
        continue;
      }
      const op =
        t.t === 'p' && BINARY_PREC[t.v] !== undefined
          ? t.v
          : t.t === 'id' && WORD_BINARY.has(t.v)
            ? t.v
            : undefined;
      if (op === undefined) break;
      const prec = BINARY_PREC[op];
      if (prec < minPrec) break;
      this.next();
      const right = this.parseExpr(op === '**' ? prec : prec + 1);
      left = { kind: 'binary', op, left, right };
    }
    return left;
  }

  private parsePrefix(): Expr {
    const t = this.next();
    if (t.t === 'num') return { kind: 'num', value: t.v };
    if (t.t === 'str') return { kind: 'str', value: t.v };
    if (t.t === 'id') {
      if (WORD_UNARY.has(t.v)) return this.makeUnary(t.v);
      return { kind: 'ident', name: t.v };
    }
    if (t.t === 'p') {
      if (t.v === '(') {
        const e = this.parseExpr(0);
        this.expectPunct(')');
        return e;
      }
      if (t.v === '[') return this.parseArray();
      if (t.v === '{') return this.parseObject();
      if (t.v === '-' || t.v === '+' || t.v === '!' || t.v === '~') {
        return this.makeUnary(t.v);
      }
    }
    throw new SyntaxError(`unexpected token ${JSON.stringify(t)}`);
  }

  private makeUnary(op: string): Expr {
    const arg = this.parseExpr(UNARY_PREC);
    if ((op === '-' || op === '+') && arg.kind === 'num') {
      return { kind: 'num', value: op === '-' ? -arg.value : arg.value };
    }
    return { kind: 'unary', op, argument: arg };
  }

  private parseArray(): Expr {
    const elements: Expr[] = [];
    if (this.peek()?.t === 'p' && (this.peek() as { v: string }).v === ']') {
      this.next();
      return { kind: 'array', elements };
    }
    for (;;) {
      elements.push(this.parseExpr(0));
      const sep = this.next();
      if (sep.t === 'p' && sep.v === ',') {
        if (this.peek()?.t === 'p' && (this.peek() as { v: string }).v === ']') {
          this.next();
          break;
        }
        continue;
      }
      if (sep.t === 'p' && sep.v === ']') break;
      throw new SyntaxError(`expected ',' or ']' but got ${JSON.stringify(sep)}`);
    }
    return { kind: 'array', elements };
  }

  private parseObject(): Expr {
    const props: Prop[] = [];
    if (this.peek()?.t === 'p' && (this.peek() as { v: string }).v === '}') {
      this.next();
      return { kind: 'object', props };
    }
    for (;;) {
      const keyTok = this.next();
      let key: string;
      if (keyTok.t === 'id') key = keyTok.v;
      else if (keyTok.t === 'str') key = keyTok.v;
      else if (keyTok.t === 'num') key = String(keyTok.v);
      else throw new SyntaxError(`expected object key, got ${JSON.stringify(keyTok)}`);
      this.expectPunct(':');
      const value = this.parseExpr(0);
      props.push({ key, value });
      const sep = this.next();
      if (sep.t === 'p' && sep.v === ',') {
        if (this.peek()?.t === 'p' && (this.peek() as { v: string }).v === '}') {
          this.next();
          break;
        }
        continue;
      }
      if (sep.t === 'p' && sep.v === '}') break;
      throw new SyntaxError(`expected ',' or '}' but got ${JSON.stringify(sep)}`);
    }
    return { kind: 'object', props };
  }
}

export function parse(src: string): Expr {
  return new Parser(tokenize(src)).parseProgram();
}
