import { ExpressionSyntaxError, UnknownContextKeyError } from './errors.ts';

export type ExprNode =
  | { kind: 'literal'; value: boolean }
  | { kind: 'ref'; key: string }
  | { kind: 'not'; operand: ExprNode }
  | { kind: 'and'; left: ExprNode; right: ExprNode }
  | { kind: 'or'; left: ExprNode; right: ExprNode };

type Token =
  | { type: 'ident'; value: string; position: number }
  | { type: 'and' | 'or' | 'not' | 'lparen' | 'rparen'; position: number };

const IDENT_START = /[A-Za-z_]/;
const IDENT_PART = /[A-Za-z0-9_.-]/;

function tokenize(source: string): Token[] {
  const tokens: Token[] = [];
  let index = 0;
  while (index < source.length) {
    const char = source[index];
    if (char === ' ' || char === '\t' || char === '\n' || char === '\r') {
      index += 1;
      continue;
    }
    if (source.startsWith('&&', index)) {
      tokens.push({ type: 'and', position: index });
      index += 2;
      continue;
    }
    if (source.startsWith('||', index)) {
      tokens.push({ type: 'or', position: index });
      index += 2;
      continue;
    }
    if (char === '!') {
      tokens.push({ type: 'not', position: index });
      index += 1;
      continue;
    }
    if (char === '(') {
      tokens.push({ type: 'lparen', position: index });
      index += 1;
      continue;
    }
    if (char === ')') {
      tokens.push({ type: 'rparen', position: index });
      index += 1;
      continue;
    }
    if (IDENT_START.test(char)) {
      let end = index + 1;
      while (end < source.length && IDENT_PART.test(source[end])) end += 1;
      tokens.push({ type: 'ident', value: source.slice(index, end), position: index });
      index = end;
      continue;
    }
    throw new ExpressionSyntaxError(`Unexpected character "${char}"`, index);
  }
  return tokens;
}

export function parseExpression(source: string): ExprNode {
  const tokens = tokenize(source);
  let cursor = 0;

  const peek = (): Token | undefined => tokens[cursor];
  const consume = (): Token => {
    const token = tokens[cursor];
    cursor += 1;
    return token;
  };

  const parseOr = (): ExprNode => {
    let left = parseAnd();
    while (peek()?.type === 'or') {
      consume();
      left = { kind: 'or', left, right: parseAnd() };
    }
    return left;
  };

  const parseAnd = (): ExprNode => {
    let left = parseUnary();
    while (peek()?.type === 'and') {
      consume();
      left = { kind: 'and', left, right: parseUnary() };
    }
    return left;
  };

  const parseUnary = (): ExprNode => {
    if (peek()?.type === 'not') {
      consume();
      return { kind: 'not', operand: parseUnary() };
    }
    return parsePrimary();
  };

  const parsePrimary = (): ExprNode => {
    const token = peek();
    if (!token) {
      const position = tokens.length > 0 ? tokens[tokens.length - 1].position + 1 : 0;
      throw new ExpressionSyntaxError('Unexpected end of expression', position);
    }
    if (token.type === 'ident') {
      consume();
      if (token.value === 'true') return { kind: 'literal', value: true };
      if (token.value === 'false') return { kind: 'literal', value: false };
      return { kind: 'ref', key: token.value };
    }
    if (token.type === 'lparen') {
      consume();
      const inner = parseOr();
      const closing = peek();
      if (!closing || closing.type !== 'rparen') {
        throw new ExpressionSyntaxError('Missing closing parenthesis', token.position);
      }
      consume();
      return inner;
    }
    throw new ExpressionSyntaxError('Expected identifier, literal, or "("', token.position);
  };

  if (tokens.length === 0) {
    throw new ExpressionSyntaxError('Empty expression', 0);
  }
  const root = parseOr();
  const leftover = peek();
  if (leftover) {
    throw new ExpressionSyntaxError('Unexpected trailing token', leftover.position);
  }
  return root;
}

export function evaluateExpression(node: ExprNode, resolve: (key: string) => boolean): boolean {
  switch (node.kind) {
    case 'literal':
      return node.value;
    case 'ref':
      return resolve(node.key);
    case 'not':
      return !evaluateExpression(node.operand, resolve);
    case 'and':
      return evaluateExpression(node.left, resolve) && evaluateExpression(node.right, resolve);
    case 'or':
      return evaluateExpression(node.left, resolve) || evaluateExpression(node.right, resolve);
  }
}

export function collectKeys(node: ExprNode, into: Set<string> = new Set()): Set<string> {
  switch (node.kind) {
    case 'ref':
      into.add(node.key);
      break;
    case 'not':
      collectKeys(node.operand, into);
      break;
    case 'and':
    case 'or':
      collectKeys(node.left, into);
      collectKeys(node.right, into);
      break;
  }
  return into;
}

export function strictResolver(context: ReadonlyMap<string, boolean>): (key: string) => boolean {
  return (key) => {
    const value = context.get(key);
    if (value === undefined) throw new UnknownContextKeyError(key);
    return value;
  };
}
