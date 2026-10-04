// Recursive-descent parser for the expression language.
//
// Grammar (loosest to tightest binding):
//   ite   := bitor ('?' ite ':' ite)?
//   bitor := bitxor ('|' bitxor)*
//   bitxor:= bitand ('^' bitand)*
//   bitand:= shift ('&' shift)*
//   shift := addsub (('<<'|'>>') addsub)*
//   addsub:= unary (('+'|'-') unary)*
//   unary := ('~'|'-') unary | atom
//   atom  := number | ident | '(' ite ')'
// Numbers: decimal, 0x hex, 0b binary. Idents: [A-Za-z_][A-Za-z0-9_]*.

import { kBin, kConst, kIte, kNeg, kNot, kVar } from './ast.ts';
import type { Expr } from './ast.ts';

export class ParseError extends Error {}

type Tok =
  | { t: 'num'; v: bigint }
  | { t: 'ident'; v: string }
  | { t: 'op'; v: string }
  | { t: 'eof' };

function tokenize(src: string): Tok[] {
  const toks: Tok[] = [];
  let i = 0;
  const isDigit = (c: string) => c >= '0' && c <= '9';
  const isAlpha = (c: string) => /[A-Za-z_]/.test(c);
  while (i < src.length) {
    const c = src[i];
    if (c === ' ' || c === '\t' || c === '\n' || c === '\r') { i++; continue; }
    if (isDigit(c)) {
      let j = i;
      if (c === '0' && (src[i + 1] === 'x' || src[i + 1] === 'X')) {
        j = i + 2;
        while (j < src.length && /[0-9a-fA-F]/.test(src[j])) j++;
        if (j === i + 2) throw new ParseError(`bad hex literal at ${i}`);
        toks.push({ t: 'num', v: BigInt(src.slice(i, j)) });
      } else if (c === '0' && (src[i + 1] === 'b' || src[i + 1] === 'B')) {
        j = i + 2;
        while (j < src.length && (src[j] === '0' || src[j] === '1')) j++;
        if (j === i + 2) throw new ParseError(`bad binary literal at ${i}`);
        toks.push({ t: 'num', v: BigInt(src.slice(i, j)) });
      } else {
        while (j < src.length && isDigit(src[j])) j++;
        toks.push({ t: 'num', v: BigInt(src.slice(i, j)) });
      }
      i = j;
      continue;
    }
    if (isAlpha(c)) {
      let j = i;
      while (j < src.length && /[A-Za-z0-9_]/.test(src[j])) j++;
      toks.push({ t: 'ident', v: src.slice(i, j) });
      i = j;
      continue;
    }
    const two = src.slice(i, i + 2);
    if (two === '<<' || two === '>>') {
      toks.push({ t: 'op', v: two });
      i += 2;
      continue;
    }
    if ('&|^+-~()?:'.includes(c)) {
      toks.push({ t: 'op', v: c });
      i++;
      continue;
    }
    throw new ParseError(`unexpected character '${c}' at ${i}`);
  }
  toks.push({ t: 'eof' });
  return toks;
}

export function parse(src: string): Expr {
  const toks = tokenize(src);
  let pos = 0;
  const peek = (): Tok => toks[pos];
  const next = (): Tok => toks[pos++];
  const expectOp = (v: string): void => {
    const t = next();
    if (t.t !== 'op' || t.v !== v) throw new ParseError(`expected '${v}', got ${JSON.stringify(t)}`);
  };

  const parseIte = (): Expr => {
    const cond = parseBinOp(0);
    const t = peek();
    if (t.t === 'op' && t.v === '?') {
      next();
      const then = parseIte();
      expectOp(':');
      const els = parseIte();
      return kIte(cond, then, els);
    }
    return cond;
  };

  const levels: string[][] = [
    ['|'],
    ['^'],
    ['&'],
    ['<<', '>>'],
    ['+', '-'],
  ];
  const opName: Record<string, Parameters<typeof kBin>[0]> = {
    '|': 'or', '^': 'xor', '&': 'and', '<<': 'shl', '>>': 'shr', '+': 'add', '-': 'sub',
  };

  const parseBinOp = (level: number): Expr => {
    if (level >= levels.length) return parseUnary();
    let lhs = parseBinOp(level + 1);
    for (;;) {
      const t = peek();
      if (t.t === 'op' && levels[level].includes(t.v)) {
        next();
        const rhs = parseBinOp(level + 1);
        lhs = kBin(opName[t.v], lhs, rhs);
      } else {
        return lhs;
      }
    }
  };

  const parseUnary = (): Expr => {
    const t = peek();
    if (t.t === 'op' && t.v === '~') { next(); return kNot(parseUnary()); }
    if (t.t === 'op' && t.v === '-') { next(); return kNeg(parseUnary()); }
    return parseAtom();
  };

  const parseAtom = (): Expr => {
    const t = next();
    if (t.t === 'num') return kConst(t.v);
    if (t.t === 'ident') return kVar(t.v);
    if (t.t === 'op' && t.v === '(') {
      const e = parseIte();
      expectOp(')');
      return e;
    }
    throw new ParseError(`unexpected token ${JSON.stringify(t)}`);
  };

  const e = parseIte();
  if (peek().t !== 'eof') throw new ParseError(`trailing input at token ${JSON.stringify(peek())}`);
  return e;
}
