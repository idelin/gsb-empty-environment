import type { BinaryOp, Expr } from './ast.ts';
import { ParseError } from './errors.ts';
import { isKeyword, tokenize, type Token } from './lexer.ts';

const MAX_PARSE_DEPTH = 200;
const MAX_ARGS = 32;

const PRECEDENCE: Readonly<Record<string, number>> = {
  '||': 1,
  '&&': 2,
  '==': 3,
  '!=': 3,
  '<': 4,
  '<=': 4,
  '>': 4,
  '>=': 4,
  '+': 5,
  '-': 5,
  '*': 6,
  '/': 6,
  '%': 6,
};

export function parse(source: string): Expr {
  const tokens = tokenize(source);
  const state = { pos: 0 };

  const peek = (): Token => tokens[state.pos]!;
  const next = (): Token => tokens[state.pos++]!;

  const expect = (type: Token['type'], value?: string): Token => {
    const tok = next();
    if (tok.type !== type || (value !== undefined && tok.value !== value)) {
      throw new ParseError(
        `Expected ${value ?? type} but found '${tok.value || tok.type}' at position ${tok.pos}`,
      );
    }
    return tok;
  };

  const parsePrimary = (depth: number): Expr => {
    if (depth > MAX_PARSE_DEPTH) {
      throw new ParseError(`Expression nesting exceeds ${MAX_PARSE_DEPTH} levels`);
    }
    const tok = next();

    if (tok.type === 'number') {
      return { kind: 'Literal', value: Number(tok.value) };
    }
    if (tok.type === 'string') {
      return { kind: 'Literal', value: tok.value };
    }
    if (tok.type === 'identifier') {
      if (isKeyword(tok.value)) {
        const value = tok.value === 'true' ? true : tok.value === 'false' ? false : null;
        return { kind: 'Literal', value };
      }
      if (peek().type === 'punct' && peek().value === '(') {
        next();
        const args: Expr[] = [];
        if (!(peek().type === 'punct' && peek().value === ')')) {
          for (;;) {
            if (args.length >= MAX_ARGS) {
              throw new ParseError(`Call exceeds ${MAX_ARGS} arguments`);
            }
            args.push(parseBinary(0, depth + 1));
            if (peek().type === 'punct' && peek().value === ',') {
              next();
              continue;
            }
            break;
          }
        }
        expect('punct', ')');
        return { kind: 'Call', callee: tok.value, args };
      }
      return { kind: 'Identifier', name: tok.value };
    }
    if (tok.type === 'operator' && (tok.value === '!' || tok.value === '-')) {
      const operand = parseBinary(7, depth + 1);
      return { kind: 'Unary', op: tok.value, operand };
    }
    if (tok.type === 'punct' && tok.value === '(') {
      const inner = parseBinary(0, depth + 1);
      expect('punct', ')');
      return inner;
    }
    throw new ParseError(`Unexpected token '${tok.value || tok.type}' at position ${tok.pos}`);
  };

  const parseBinary = (minPrecedence: number, depth: number): Expr => {
    let left = parsePrimary(depth);
    for (;;) {
      const tok = peek();
      if (tok.type !== 'operator') break;
      const precedence = PRECEDENCE[tok.value];
      if (precedence === undefined || precedence < minPrecedence) break;
      next();
      const right = parseBinary(precedence + 1, depth);
      left = { kind: 'Binary', op: tok.value as BinaryOp, left, right };
    }
    return left;
  };

  const expr = parseBinary(0, 0);
  const tail = peek();
  if (tail.type !== 'eof') {
    throw new ParseError(`Unexpected trailing token '${tail.value}' at position ${tail.pos}`);
  }
  return expr;
}
