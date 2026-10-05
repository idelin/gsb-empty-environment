import { TrimError } from './errors.ts';

export type Expr =
  | { kind: 'const'; value: boolean }
  | { kind: 'var'; name: string }
  | { kind: 'not'; operand: Expr }
  | { kind: 'and'; left: Expr; right: Expr }
  | { kind: 'or'; left: Expr; right: Expr };

type Token =
  | { kind: 'ident'; name: string }
  | { kind: 'and' }
  | { kind: 'or' }
  | { kind: 'not' }
  | { kind: 'lparen' }
  | { kind: 'rparen' };

function isIdentStart(ch: string): boolean {
  return /[A-Za-z_]/.test(ch);
}

function isIdentPart(ch: string): boolean {
  return /[A-Za-z0-9_]/.test(ch);
}

function tokenize(input: string, line: number): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  while (i < input.length) {
    const ch = input[i];
    if (ch === ' ' || ch === '\t') {
      i++;
    } else if (ch === '&' && input[i + 1] === '&') {
      tokens.push({ kind: 'and' });
      i += 2;
    } else if (ch === '|' && input[i + 1] === '|') {
      tokens.push({ kind: 'or' });
      i += 2;
    } else if (ch === '!') {
      tokens.push({ kind: 'not' });
      i++;
    } else if (ch === '(') {
      tokens.push({ kind: 'lparen' });
      i++;
    } else if (ch === ')') {
      tokens.push({ kind: 'rparen' });
      i++;
    } else if (isIdentStart(ch)) {
      let j = i + 1;
      while (j < input.length && isIdentPart(input[j])) j++;
      tokens.push({ kind: 'ident', name: input.slice(i, j) });
      i = j;
    } else {
      throw new TrimError('INVALID_EXPRESSION', line, `unexpected character '${ch}' in condition`);
    }
  }
  return tokens;
}

/** Parses a boolean condition: or := and ( '||' and )* ; and := unary ( '&&' unary )* */
export function parseExpr(input: string, line: number): Expr {
  const tokens = tokenize(input, line);
  let pos = 0;

  function peek(): Token | undefined {
    return tokens[pos];
  }

  function parseOr(): Expr {
    let left = parseAnd();
    while (peek()?.kind === 'or') {
      pos++;
      left = { kind: 'or', left, right: parseAnd() };
    }
    return left;
  }

  function parseAnd(): Expr {
    let left = parseUnary();
    while (peek()?.kind === 'and') {
      pos++;
      left = { kind: 'and', left, right: parseUnary() };
    }
    return left;
  }

  function parseUnary(): Expr {
    if (peek()?.kind === 'not') {
      pos++;
      return { kind: 'not', operand: parseUnary() };
    }
    return parsePrimary();
  }

  function parsePrimary(): Expr {
    const tok = peek();
    if (!tok) {
      throw new TrimError('INVALID_EXPRESSION', line, 'unexpected end of condition');
    }
    if (tok.kind === 'lparen') {
      pos++;
      const inner = parseOr();
      if (peek()?.kind !== 'rparen') {
        throw new TrimError('INVALID_EXPRESSION', line, "missing ')' in condition");
      }
      pos++;
      return inner;
    }
    if (tok.kind === 'ident') {
      pos++;
      if (tok.name === 'true') return { kind: 'const', value: true };
      if (tok.name === 'false') return { kind: 'const', value: false };
      return { kind: 'var', name: tok.name };
    }
    throw new TrimError('INVALID_EXPRESSION', line, 'unexpected token in condition');
  }

  const expr = parseOr();
  if (pos !== tokens.length) {
    throw new TrimError('INVALID_EXPRESSION', line, 'trailing tokens in condition');
  }
  return expr;
}

/**
 * Short-circuit evaluation: the right side of '&&' / '||' is only evaluated
 * when needed, so unknown variables there never raise.
 */
export function evalExpr(expr: Expr, vars: Record<string, boolean>, line: number): boolean {
  switch (expr.kind) {
    case 'const':
      return expr.value;
    case 'not':
      return !evalExpr(expr.operand, vars, line);
    case 'and':
      return evalExpr(expr.left, vars, line) && evalExpr(expr.right, vars, line);
    case 'or':
      return evalExpr(expr.left, vars, line) || evalExpr(expr.right, vars, line);
    case 'var': {
      if (!Object.prototype.hasOwnProperty.call(vars, expr.name)) {
        throw new TrimError('UNKNOWN_VARIABLE', line, `unknown variable '${expr.name}' in condition`);
      }
      return Boolean(vars[expr.name]);
    }
  }
}
