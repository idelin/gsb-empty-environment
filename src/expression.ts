import { TemplateError } from './errors.ts';
import { locationAt, type SourceLocation } from './location.ts';
import { assertSafeKey } from './safe.ts';

export interface ExpressionBudget {
  maxNodes: number;
  maxDepth: number;
  lineStarts: readonly number[];
  nodes: number;
}

export type LiteralValue = string | number | boolean | null | undefined;

export interface LiteralExpression {
  type: 'literal';
  value: LiteralValue;
  location: SourceLocation;
}

export interface PathExpression {
  type: 'path';
  root: string;
  parentSteps: number;
  segments: string[];
  location: SourceLocation;
}

export interface UnaryExpression {
  type: 'unary';
  operator: '!' | '-' | '+';
  argument: Expression;
  location: SourceLocation;
}

export interface BinaryExpression {
  type: 'binary';
  operator:
    | '=='
    | '!='
    | '==='
    | '!=='
    | '<'
    | '<='
    | '>'
    | '>='
    | '&&'
    | '||'
    | '+'
    | '-'
    | '*'
    | '/';
  left: Expression;
  right: Expression;
  location: SourceLocation;
}

export interface FilterCall {
  name: string;
  arguments: Expression[];
  location: SourceLocation;
}

export interface PipelineExpression {
  type: 'pipeline';
  value: Expression;
  filters: FilterCall[];
  location: SourceLocation;
}

export type Expression =
  | LiteralExpression
  | PathExpression
  | UnaryExpression
  | BinaryExpression
  | PipelineExpression;

type TokenType =
  | 'identifier'
  | 'special'
  | 'number'
  | 'string'
  | 'operator'
  | 'punct'
  | 'parent'
  | 'eof';

interface Token {
  type: TokenType;
  value: string | number;
  offset: number;
}

function tokenLocation(budget: ExpressionBudget, offset: number): SourceLocation {
  return locationAt(budget.lineStarts, offset);
}

function countNode(budget: ExpressionBudget, offset: number): void {
  budget.nodes += 1;
  if (budget.nodes > budget.maxNodes) {
    throw new TemplateError(tokenLocation(budget, offset), 'BUDGET_EXCEEDED');
  }
}

const OPERATORS = [
  '===',
  '!==',
  '==',
  '!=',
  '<=',
  '>=',
  '&&',
  '||',
  '<',
  '>',
  '!',
  '+',
  '-',
  '*',
  '/',
] as const;

const PUNCTUATION = new Set(['.', '(', ')', '[', ']', ',', ':', '|']);

function isIdentifierStart(character: string): boolean {
  return /[A-Za-z_$]/.test(character);
}

function isIdentifierPart(character: string): boolean {
  return /[A-Za-z0-9_$]/.test(character);
}

function decodeString(source: string, start: number, budget: ExpressionBudget): Token {
  const quote = source[start];
  let index = start + 1;
  let value = '';

  while (index < source.length) {
    const character = source[index];
    if (character === quote) {
      return { type: 'string', value, offset: start };
    }
    if (character !== '\\') {
      value += character;
      index += 1;
      continue;
    }

    index += 1;
    if (index >= source.length) {
      break;
    }
    const escaped = source[index];
    if (escaped === 'n') value += '\n';
    else if (escaped === 'r') value += '\r';
    else if (escaped === 't') value += '\t';
    else if (escaped === 'b') value += '\b';
    else if (escaped === 'f') value += '\f';
    else if (escaped === 'v') value += '\v';
    else if (escaped === '0') value += '\0';
    else if (escaped === '\\') value += '\\';
    else if (escaped === '"') value += '"';
    else if (escaped === "'") value += "'";
    else if (escaped === 'u') {
      const hex = source.slice(index + 1, index + 5);
      if (!/^[0-9A-Fa-f]{4}$/.test(hex)) {
        throw new TemplateError(
          tokenLocation(budget, index),
          'SYNTAX',
        );
      }
      value += String.fromCharCode(Number.parseInt(hex, 16));
      index += 4;
    } else if (escaped === 'x') {
      const hex = source.slice(index + 1, index + 3);
      if (!/^[0-9A-Fa-f]{2}$/.test(hex)) {
        throw new TemplateError(
          tokenLocation(budget, index),
          'SYNTAX',
        );
      }
      value += String.fromCharCode(Number.parseInt(hex, 16));
      index += 2;
    } else {
      value += escaped;
    }
    index += 1;
  }

  throw new TemplateError(tokenLocation(budget, start), 'SYNTAX');
}

function tokenize(source: string, baseOffset: number, budget: ExpressionBudget): Token[] {
  const tokens: Token[] = [];
  let index = 0;

  while (index < source.length) {
    const character = source[index];
    if (/\s/.test(character)) {
      index += 1;
      continue;
    }
    const offset = baseOffset + index;

    if (character === "'" || character === '"') {
      const end = nextStringEnd(source, index);
      const token = decodeString(source, index, budget);
      tokens.push({
        type: 'string',
        value: token.value,
        offset: baseOffset + index,
      });
      index = end + 1;
      continue;
    }

    if (source.startsWith('../', index)) {
      tokens.push({ type: 'parent', value: '../', offset });
      index += 3;
      continue;
    }

    if (character === '@') {
      let end = index + 1;
      if (end < source.length && isIdentifierStart(source[end])) {
        end += 1;
        while (end < source.length && isIdentifierPart(source[end])) {
          end += 1;
        }
      }
      if (end === index + 1) {
        throw new TemplateError(tokenLocation(budget, offset), 'SYNTAX');
      }
      tokens.push({
        type: 'special',
        value: source.slice(index, end),
        offset,
      });
      index = end;
      continue;
    }

    if (/[0-9]/.test(character)) {
      const match = /^[0-9]+(?:\.[0-9]+)?/.exec(source.slice(index));
      if (match !== null) {
        tokens.push({
          type: 'number',
          value: Number(match[0]),
          offset,
        });
        index += match[0].length;
        continue;
      }
    }

    if (isIdentifierStart(character)) {
      let end = index + 1;
      while (end < source.length && isIdentifierPart(source[end])) {
        end += 1;
      }
      tokens.push({
        type: 'identifier',
        value: source.slice(index, end),
        offset,
      });
      index = end;
      continue;
    }

    let matchedOperator: string | undefined;
    for (const operator of OPERATORS) {
      if (source.startsWith(operator, index)) {
        matchedOperator = operator;
        break;
      }
    }
    if (matchedOperator !== undefined) {
      tokens.push({ type: 'operator', value: matchedOperator, offset });
      index += matchedOperator.length;
      continue;
    }

    if (PUNCTUATION.has(character)) {
      tokens.push({ type: 'punct', value: character, offset });
      index += 1;
      continue;
    }

    throw new TemplateError(tokenLocation(budget, offset), 'SYNTAX');
  }

  tokens.push({ type: 'eof', value: '', offset: baseOffset + source.length });
  return tokens;
}

/**
 * `decodeString` returns the offset of its opening quote. This helper scans
 * the same token once more to locate the closing quote without relying on
 * JavaScript string semantics.
 */
function nextStringEnd(source: string, start: number): number {
  const quote = source[start];
  let index = start + 1;
  while (index < source.length) {
    if (source[index] === '\\') {
      index += 2;
      continue;
    }
    if (source[index] === quote) {
      return index;
    }
    index += 1;
  }
  return source.length - 1;
}

const BINARY_PRECEDENCE: Record<string, number> = {
  '||': 1,
  '&&': 2,
  '==': 3,
  '!=': 3,
  '===': 3,
  '!==': 3,
  '<': 4,
  '<=': 4,
  '>': 4,
  '>=': 4,
  '+': 5,
  '-': 5,
  '*': 6,
  '/': 6,
};

class ExpressionParser {
  private readonly tokens: Token[];
  private readonly budget: ExpressionBudget;
  private index = 0;
  private depth = 0;

  constructor(tokens: Token[], budget: ExpressionBudget) {
    this.tokens = tokens;
    this.budget = budget;
  }

  parse(): Expression {
    this.enter(this.current().offset);
    const expression = this.parsePipeline();
    this.leave();
    if (this.current().type !== 'eof') {
      throw new TemplateError(
        tokenLocation(this.budget, this.current().offset),
        'SYNTAX',
      );
    }
    return expression;
  }

  private current(): Token {
    return this.tokens[Math.min(this.index, this.tokens.length - 1)];
  }

  private peek(distance = 1): Token {
    return this.tokens[Math.min(this.index + distance, this.tokens.length - 1)];
  }

  private consume(): Token {
    const token = this.current();
    if (token.type !== 'eof') {
      this.index += 1;
    }
    return token;
  }

  private enter(offset: number): void {
    this.depth += 1;
    if (this.depth > this.budget.maxDepth) {
      throw new TemplateError(
        tokenLocation(this.budget, offset),
        'BUDGET_EXCEEDED',
      );
    }
  }

  private leave(): void {
    this.depth -= 1;
  }

  private node(offset: number): void {
    countNode(this.budget, offset);
  }

  private parsePipeline(): Expression {
    const value = this.parseBinary(0);
    const filters: FilterCall[] = [];

    while (
      this.current().type === 'punct' &&
      this.current().value === '|'
    ) {
      const pipe = this.consume();
      const nameToken = this.current();
      if (nameToken.type !== 'identifier') {
        throw new TemplateError(
          tokenLocation(this.budget, nameToken.offset),
          'SYNTAX',
        );
      }
      this.consume();
      const location = tokenLocation(this.budget, pipe.offset);
      assertSafeKey(String(nameToken.value), location);
      this.node(pipe.offset);

      const args: Expression[] = [];
      if (
        this.current().type === 'punct' &&
        this.current().value === '('
      ) {
        this.consume();
        if (
          !(this.current().type === 'punct' && this.current().value === ')')
        ) {
          while (true) {
            if (
              this.current().type === 'punct' &&
              this.current().value === '|'
            ) {
              throw new TemplateError(
                tokenLocation(this.budget, this.current().offset),
                'SYNTAX',
              );
            }
            args.push(this.parseBinary(0));
            if (
              this.current().type === 'punct' &&
              this.current().value === ','
            ) {
              this.consume();
              continue;
            }
            break;
          }
        }
        if (
          !(this.current().type === 'punct' && this.current().value === ')')
        ) {
          throw new TemplateError(
            tokenLocation(this.budget, this.current().offset),
            'SYNTAX',
          );
        }
        this.consume();
      } else if (
        this.current().type === 'punct' &&
        this.current().value === ':'
      ) {
        this.consume();
        while (true) {
          args.push(this.parseBinary(0));
          if (
            this.current().type === 'punct' &&
            this.current().value === ','
          ) {
            this.consume();
            continue;
          }
          if (
            this.current().type === 'punct' &&
            this.current().value === ':'
          ) {
            this.consume();
            continue;
          }
          break;
        }
      }

      filters.push({ name: String(nameToken.value), arguments: args, location });
    }

    if (filters.length === 0) {
      return value;
    }
    this.node(value.location.offset);
    return {
      type: 'pipeline',
      value,
      filters,
      location: value.location,
    };
  }

  private parseBinary(minimumPrecedence: number): Expression {
    const offset = this.current().offset;
    this.enter(offset);
    let left = this.parseUnary();

    while (true) {
      const token = this.current();
      let operator: string | undefined;
      if (token.type === 'operator') {
        operator = String(token.value);
      } else if (token.type === 'identifier') {
        const word = String(token.value);
        if (word === 'and') operator = '&&';
        else if (word === 'or') operator = '||';
      }

      if (operator === undefined) {
        break;
      }
      const precedence = BINARY_PRECEDENCE[operator];
      if (precedence === undefined || precedence < minimumPrecedence) {
        break;
      }

      this.consume();
      const right = this.parseBinary(precedence + 1);
      this.node(token.offset);
      left = {
        type: 'binary',
        operator: operator as BinaryExpression['operator'],
        left,
        right,
        location: tokenLocation(this.budget, token.offset),
      };
    }

    this.leave();
    return left;
  }

  private parseUnary(): Expression {
    const token = this.current();
    const operator =
      (token.type === 'operator' &&
        (token.value === '!' || token.value === '-' || token.value === '+') &&
        token.value) ||
      (token.type === 'identifier' && token.value === 'not' && '!');

    if (operator !== false && operator !== undefined) {
      this.consume();
      const argument = this.parseUnary();
      this.node(token.offset);
      return {
        type: 'unary',
        operator: operator as UnaryExpression['operator'],
        argument,
        location: tokenLocation(this.budget, token.offset),
      };
    }

    return this.parsePostfix();
  }

  private parsePostfix(): Expression {
    let expression = this.parsePrimary();

    while (true) {
      const token = this.current();
      if (token.type === 'punct' && token.value === '(') {
        throw new TemplateError(
          tokenLocation(this.budget, token.offset),
          'FORBIDDEN_ACCESS',
        );
      }

      if (token.type === 'punct' && token.value === '.') {
        if (expression.type !== 'path') {
          throw new TemplateError(
            tokenLocation(this.budget, token.offset),
            'FORBIDDEN_ACCESS',
          );
        }
        this.consume();
        const segmentToken = this.current();
        let segment: string;
        if (segmentToken.type === 'identifier') {
          segment = String(segmentToken.value);
        } else if (segmentToken.type === 'number') {
          const numeric = Number(segmentToken.value);
          if (!Number.isSafeInteger(numeric) || numeric < 0) {
            throw new TemplateError(
              tokenLocation(this.budget, segmentToken.offset),
              'FORBIDDEN_ACCESS',
            );
          }
          segment = String(numeric);
        } else {
          throw new TemplateError(
            tokenLocation(this.budget, segmentToken.offset),
            'FORBIDDEN_ACCESS',
          );
        }
        this.consume();
        assertSafeKey(segment, tokenLocation(this.budget, segmentToken.offset));
        expression.segments.push(segment);
        continue;
      }

      if (token.type === 'punct' && token.value === '[') {
        if (expression.type !== 'path') {
          throw new TemplateError(
            tokenLocation(this.budget, token.offset),
            'FORBIDDEN_ACCESS',
          );
        }
        this.consume();
        const keyToken = this.current();
        let segment: string;
        if (keyToken.type === 'string') {
          segment = String(keyToken.value);
        } else if (keyToken.type === 'number') {
          const numeric = Number(keyToken.value);
          if (!Number.isSafeInteger(numeric) || numeric < 0) {
            throw new TemplateError(
              tokenLocation(this.budget, keyToken.offset),
              'FORBIDDEN_ACCESS',
            );
          }
          segment = String(numeric);
        } else {
          // Computed keys could assemble forbidden names at runtime.
          throw new TemplateError(
            tokenLocation(this.budget, keyToken.offset),
            'FORBIDDEN_ACCESS',
          );
        }
        this.consume();
        if (
          !(this.current().type === 'punct' && this.current().value === ']')
        ) {
          throw new TemplateError(
            tokenLocation(this.budget, this.current().offset),
            'SYNTAX',
          );
        }
        this.consume();
        assertSafeKey(segment, tokenLocation(this.budget, keyToken.offset));
        expression.segments.push(segment);
        continue;
      }

      break;
    }

    return expression;
  }

  private parsePrimary(): Expression {
    const token = this.current();
    if (token.type === 'eof') {
      throw new TemplateError(
        tokenLocation(this.budget, token.offset),
        'SYNTAX',
      );
    }

    if (token.type === 'string') {
      this.consume();
      this.node(token.offset);
      return {
        type: 'literal',
        value: String(token.value),
        location: tokenLocation(this.budget, token.offset),
      };
    }

    if (token.type === 'number') {
      this.consume();
      this.node(token.offset);
      return {
        type: 'literal',
        value: Number(token.value),
        location: tokenLocation(this.budget, token.offset),
      };
    }

    if (token.type === 'identifier') {
      const word = String(token.value);
      if (word === 'true' || word === 'false') {
        this.consume();
        this.node(token.offset);
        return {
          type: 'literal',
          value: word === 'true',
          location: tokenLocation(this.budget, token.offset),
        };
      }
      if (word === 'null' || word === 'undefined') {
        this.consume();
        this.node(token.offset);
        return {
          type: 'literal',
          value: word === 'null' ? null : undefined,
          location: tokenLocation(this.budget, token.offset),
        };
      }

      this.consume();
      const location = tokenLocation(this.budget, token.offset);
      assertSafeKey(word, location);
      this.node(token.offset);
      return {
        type: 'path',
        root: word,
        parentSteps: 0,
        segments: [],
        location,
      };
    }

    if (token.type === 'special') {
      this.consume();
      const value = String(token.value);
      if (
        value !== '@root' &&
        value !== '@index' &&
        value !== '@first' &&
        value !== '@last' &&
        value !== '@key'
      ) {
        throw new TemplateError(
          tokenLocation(this.budget, token.offset),
          'FORBIDDEN_ACCESS',
        );
      }
      this.node(token.offset);
      return {
        type: 'path',
        root: value,
        parentSteps: 0,
        segments: [],
        location: tokenLocation(this.budget, token.offset),
      };
    }

    if (token.type === 'parent') {
      let parentSteps = 0;
      let offset = token.offset;
      while (this.current().type === 'parent') {
        offset = this.current().offset;
        this.consume();
        parentSteps += 1;
      }
      if (this.current().type === 'identifier') {
        const rootToken = this.consume();
        const root = String(rootToken.value);
        const location = tokenLocation(this.budget, rootToken.offset);
        assertSafeKey(root, location);
        this.node(offset);
        return {
          type: 'path',
          root,
          parentSteps,
          segments: [],
          location,
        };
      }
      if (this.current().type === 'special' && this.current().value === '@root') {
        this.consume();
        this.node(offset);
        return {
          type: 'path',
          root: '@root',
          parentSteps,
          segments: [],
          location: tokenLocation(this.budget, offset),
        };
      }
      this.node(offset);
      return {
        type: 'path',
        root: 'this',
        parentSteps,
        segments: [],
        location: tokenLocation(this.budget, offset),
      };
    }

    if (token.type === 'punct' && token.value === '.') {
      this.consume();
      this.node(token.offset);
      const path: PathExpression = {
        type: 'path',
        root: 'this',
        parentSteps: 0,
        segments: [],
        location: tokenLocation(this.budget, token.offset),
      };
      if (this.current().type === 'identifier') {
        const segmentToken = this.consume();
        const segment = String(segmentToken.value);
        assertSafeKey(segment, tokenLocation(this.budget, segmentToken.offset));
        path.segments.push(segment);
      }
      return path;
    }

    if (token.type === 'punct' && token.value === '(') {
      this.consume();
      const expression = this.parsePipeline();
      if (
        !(this.current().type === 'punct' && this.current().value === ')')
      ) {
        throw new TemplateError(
          tokenLocation(this.budget, this.current().offset),
          'SYNTAX',
        );
      }
      this.consume();
      return expression;
    }

    throw new TemplateError(
      tokenLocation(this.budget, token.offset),
      'SYNTAX',
    );
  }
}

export function parseExpression(
  source: string,
  baseOffset: number,
  budget: ExpressionBudget,
): Expression {
  const tokens = tokenize(source, baseOffset, budget);
  return new ExpressionParser(tokens, budget).parse();
}

export function countExpressionNodes(expression: Expression): number {
  switch (expression.type) {
    case 'literal':
    case 'path':
      return 1;
    case 'unary':
      return 1 + countExpressionNodes(expression.argument);
    case 'binary':
      return (
        1 +
        countExpressionNodes(expression.left) +
        countExpressionNodes(expression.right)
      );
    case 'pipeline': {
      let count = 1 + countExpressionNodes(expression.value);
      for (const filter of expression.filters) {
        count += 1;
        for (const argument of filter.arguments) {
          count += countExpressionNodes(argument);
        }
      }
      return count;
    }
  }
}
