// AST and s-expression parser for the mini language.
//
//   program := form*
//   form    := (defun name (param*) expr+)        -- function definition
//            | expr                               -- top-level expression
//   expr    := number | string | true | false | symbol
//            | (if cond then else)
//            | (begin expr+)
//            | (let (name value) body)
//            | (error msg)
//            | (prim-op expr*)                    -- + - * div mod < <= > >= = not and or
//            | (fname expr*)                      -- user function call

import { isPrimOp } from './runtime';

export interface Pos {
  line: number;
  col: number;
}

export type Expr =
  | { kind: 'num'; value: number; pos: Pos }
  | { kind: 'bool'; value: boolean; pos: Pos }
  | { kind: 'str'; value: string; pos: Pos }
  | { kind: 'var'; name: string; pos: Pos }
  | { kind: 'if'; cond: Expr; then: Expr; else: Expr; pos: Pos }
  | { kind: 'begin'; exprs: Expr[]; pos: Pos }
  | { kind: 'let'; name: string; value: Expr; body: Expr; pos: Pos }
  | { kind: 'error'; message: Expr; pos: Pos }
  | { kind: 'prim'; op: string; args: Expr[]; pos: Pos }
  | { kind: 'call'; name: string; args: Expr[]; pos: Pos };

export interface FunDef {
  name: string;
  params: string[];
  body: Expr;
  pos: Pos;
}

export interface Program {
  defs: FunDef[];
  main: Expr[];
}

const SPECIAL_FORMS = new Set(['defun', 'if', 'begin', 'let', 'error']);

interface Tok {
  t: 'lparen' | 'rparen' | 'sym' | 'num' | 'str';
  text: string;
  pos: Pos;
}

function tokenize(src: string): Tok[] {
  const toks: Tok[] = [];
  let i = 0;
  let line = 1;
  let col = 1;
  const pos = (): Pos => ({ line, col });
  const advance = (n: number): void => {
    for (; n > 0; n--) {
      if (src[i] === '\n') { line++; col = 1; } else { col++; }
      i++;
    }
  };
  while (i < src.length) {
    const c = src[i]!;
    if (c === ';') {
      while (i < src.length && src[i] !== '\n') advance(1);
      continue;
    }
    if (/\s/.test(c)) { advance(1); continue; }
    if (c === '(') { toks.push({ t: 'lparen', text: '(', pos: pos() }); advance(1); continue; }
    if (c === ')') { toks.push({ t: 'rparen', text: ')', pos: pos() }); advance(1); continue; }
    if (c === '"') {
      const start = pos();
      advance(1);
      let text = '';
      for (;;) {
        if (i >= src.length) throw new Error(`unterminated string at ${start.line}:${start.col}`);
        const ch = src[i]!;
        if (ch === '"') { advance(1); break; }
        if (ch === '\\') {
          const next = src[i + 1];
          if (next === 'n') { text += '\n'; advance(2); continue; }
          if (next === '"' || next === '\\') { text += next; advance(2); continue; }
          throw new Error(`bad escape at ${line}:${col}`);
        }
        text += ch;
        advance(1);
      }
      toks.push({ t: 'str', text, pos: start });
      continue;
    }
    if (/[0-9]/.test(c) || (c === '-' && i + 1 < src.length && /[0-9]/.test(src[i + 1]!))) {
      const start = pos();
      let text = '';
      while (i < src.length && /[0-9.-]/.test(src[i]!)) { text += src[i]; advance(1); }
      if (!/^-?[0-9]+$/.test(text)) throw new Error(`bad number '${text}' at ${start.line}:${start.col}`);
      toks.push({ t: 'num', text, pos: start });
      continue;
    }
    const start = pos();
    let text = '';
    while (i < src.length && !/[\s()]/.test(src[i]!)) { text += src[i]; advance(1); }
    toks.push({ t: 'sym', text, pos: start });
  }
  return toks;
}

class Parser {
  private idx = 0;
  constructor(private readonly toks: Tok[]) {}

  private peek(): Tok | undefined {
    return this.toks[this.idx];
  }
  private next(): Tok {
    const t = this.toks[this.idx];
    if (t === undefined) throw new Error('unexpected end of input');
    this.idx++;
    return t;
  }
  private expect(t: Tok['t']): Tok {
    const tok = this.next();
    if (tok.t !== t) throw new Error(`expected ${t}, got '${tok.text}' at ${tok.pos.line}:${tok.pos.col}`);
    return tok;
  }
  atEnd(): boolean {
    return this.idx >= this.toks.length;
  }

  parseProgram(): Program {
    const defs: FunDef[] = [];
    const main: Expr[] = [];
    while (!this.atEnd()) {
      if (this.peek()?.t === 'lparen' && this.toks[this.idx + 1]?.text === 'defun') {
        defs.push(this.parseDefun());
      } else {
        main.push(this.parseExpr());
      }
    }
    if (main.length === 0) throw new Error('program has no top-level expression');
    return { defs, main };
  }

  private parseDefun(): FunDef {
    const open = this.expect('lparen');
    this.next(); // 'defun'
    const nameTok = this.expect('sym');
    const name = nameTok.text;
    if (SPECIAL_FORMS.has(name) || isPrimOp(name)) {
      throw new Error(`cannot redefine special form or primitive '${name}'`);
    }
    this.expect('lparen');
    const params: string[] = [];
    while (this.peek()?.t !== 'rparen') {
      params.push(this.expect('sym').text);
    }
    this.next(); // ')'
    const body: Expr[] = [];
    while (this.peek()?.t !== 'rparen') {
      body.push(this.parseExpr());
    }
    if (body.length === 0) throw new Error(`defun '${name}' has empty body`);
    this.next(); // ')'
    const bodyExpr: Expr =
      body.length === 1 ? body[0]! : { kind: 'begin', exprs: body, pos: open.pos };
    return { name, params, body: bodyExpr, pos: open.pos };
  }

  private parseExpr(): Expr {
    const tok = this.next();
    switch (tok.t) {
      case 'num':
        return { kind: 'num', value: parseInt(tok.text, 10), pos: tok.pos };
      case 'str':
        return { kind: 'str', value: tok.text, pos: tok.pos };
      case 'sym':
        if (tok.text === 'true') return { kind: 'bool', value: true, pos: tok.pos };
        if (tok.text === 'false') return { kind: 'bool', value: false, pos: tok.pos };
        return { kind: 'var', name: tok.text, pos: tok.pos };
      case 'lparen':
        return this.parseCompound(tok.pos);
      default:
        throw new Error(`unexpected '${tok.text}' at ${tok.pos.line}:${tok.pos.col}`);
    }
  }

  private parseCompound(pos: Pos): Expr {
    const head = this.next();
    if (head.t !== 'sym') throw new Error(`expected symbol after '(' at ${pos.line}:${pos.col}`);
    const rest = (): Expr[] => {
      const out: Expr[] = [];
      while (this.peek()?.t !== 'rparen') out.push(this.parseExpr());
      this.next(); // ')'
      return out;
    };
    switch (head.text) {
      case 'if': {
        const es = rest();
        if (es.length !== 3) throw new Error(`if expects 3 operands at ${pos.line}:${pos.col}`);
        return { kind: 'if', cond: es[0]!, then: es[1]!, else: es[2]!, pos };
      }
      case 'begin': {
        const es = rest();
        if (es.length === 0) throw new Error(`begin expects at least 1 expression at ${pos.line}:${pos.col}`);
        return { kind: 'begin', exprs: es, pos };
      }
      case 'let': {
        this.expect('lparen');
        const name = this.expect('sym').text;
        const value = this.parseExpr();
        this.expect('rparen');
        const body = this.parseExpr();
        this.expect('rparen');
        return { kind: 'let', name, value, body, pos };
      }
      case 'error': {
        const es = rest();
        if (es.length !== 1) throw new Error(`error expects 1 operand at ${pos.line}:${pos.col}`);
        return { kind: 'error', message: es[0]!, pos };
      }
      default: {
        const es = rest();
        if (isPrimOp(head.text)) return { kind: 'prim', op: head.text, args: es, pos };
        return { kind: 'call', name: head.text, args: es, pos };
      }
    }
  }
}

export function parseProgram(src: string): Program {
  return new Parser(tokenize(src)).parseProgram();
}
