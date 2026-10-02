import { ValidationError } from './errors.ts';
import { lex, type Token } from './lexer.ts';

export type AstNode =
  | { kind: 'num'; value: number }
  | { kind: 'str'; value: string }
  | { kind: 'bool'; value: boolean }
  | { kind: 'null' }
  | { kind: 'ident'; name: string }
  | { kind: 'unary'; op: '!' | '-'; arg: AstNode }
  | { kind: 'binary'; op: string; left: AstNode; right: AstNode }
  | { kind: 'call'; name: string; args: AstNode[] };

export interface ParseOptions {
  /** Maximum parenthesized / unary nesting depth (guards the host stack). */
  maxParseDepth?: number;
  /** Maximum number of AST nodes a single expression may contain. */
  maxNodes?: number;
}

const BINARY_PRECEDENCE: Record<string, number> = {
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

class Parser {
  private pos = 0;
  private nodes = 0;
  private readonly tokens: Token[];
  private readonly maxParseDepth: number;
  private readonly maxNodes: number;

  constructor(tokens: Token[], maxParseDepth: number, maxNodes: number) {
    this.tokens = tokens;
    this.maxParseDepth = maxParseDepth;
    this.maxNodes = maxNodes;
  }

  private peek(): Token {
    return this.tokens[this.pos];
  }

  private next(): Token {
    return this.tokens[this.pos++];
  }

  private expectPunct(value: string): void {
    const tok = this.next();
    if (tok.type !== 'punct' || tok.value !== value) {
      throw new ValidationError(`expected '${value}' at position ${tok.pos}`);
    }
  }

  private makeNode<T extends AstNode>(node: T): T {
    this.nodes++;
    if (this.nodes > this.maxNodes) {
      throw new ValidationError(`expression exceeds the ${this.maxNodes}-node AST limit`);
    }
    return node;
  }

  parseExpression(): AstNode {
    const expr = this.parseBinary(0, 0);
    const tok = this.peek();
    if (tok.type !== 'eof') {
      throw new ValidationError(`unexpected token '${tok.value}' at position ${tok.pos}`);
    }
    return expr;
  }

  private parseBinary(minPrecedence: number, depth: number): AstNode {
    let left = this.parseUnary(depth);
    for (;;) {
      const tok = this.peek();
      if (tok.type !== 'op') break;
      const precedence = BINARY_PRECEDENCE[tok.value];
      if (precedence === undefined || precedence < minPrecedence) break;
      this.next();
      const right = this.parseBinary(precedence + 1, depth);
      left = this.makeNode({ kind: 'binary', op: tok.value, left, right });
    }
    return left;
  }

  private parseUnary(depth: number): AstNode {
    if (depth > this.maxParseDepth) {
      throw new ValidationError(`expression nesting exceeds the maximum depth of ${this.maxParseDepth}`);
    }
    const tok = this.peek();
    if (tok.type === 'op' && (tok.value === '!' || tok.value === '-')) {
      this.next();
      const arg = this.parseUnary(depth + 1);
      return this.makeNode({ kind: 'unary', op: tok.value as '!' | '-', arg });
    }
    return this.parsePrimary(depth);
  }

  private parsePrimary(depth: number): AstNode {
    const tok = this.next();
    switch (tok.type) {
      case 'num':
        return this.makeNode({ kind: 'num', value: Number(tok.value) });
      case 'str':
        return this.makeNode({ kind: 'str', value: tok.value });
      case 'ident': {
        if (tok.value === 'true') return this.makeNode({ kind: 'bool', value: true });
        if (tok.value === 'false') return this.makeNode({ kind: 'bool', value: false });
        if (tok.value === 'null') return this.makeNode({ kind: 'null' });
        if (this.peek().type === 'punct' && this.peek().value === '(') {
          // Only bare-identifier calls exist in this grammar; there is no
          // member-access syntax, so `obj.method()` is unparseable by design.
          this.next();
          const args: AstNode[] = [];
          if (!(this.peek().type === 'punct' && this.peek().value === ')')) {
            for (;;) {
              args.push(this.parseBinary(0, depth + 1));
              const sep = this.next();
              if (sep.type === 'punct' && sep.value === ',') continue;
              if (sep.type === 'punct' && sep.value === ')') break;
              throw new ValidationError(`expected ',' or ')' at position ${sep.pos}`);
            }
          } else {
            this.expectPunct(')');
          }
          return this.makeNode({ kind: 'call', name: tok.value, args });
        }
        return this.makeNode({ kind: 'ident', name: tok.value });
      }
      case 'punct':
        if (tok.value === '(') {
          const inner = this.parseBinary(0, depth + 1);
          this.expectPunct(')');
          return inner;
        }
        break;
    }
    throw new ValidationError(`unexpected token '${tok.value}' at position ${tok.pos}`);
  }
}

export function parse(source: string, options: ParseOptions = {}): AstNode {
  const { maxParseDepth = 200, maxNodes = 5000 } = options;
  const parser = new Parser(lex(source), maxParseDepth, maxNodes);
  return parser.parseExpression();
}
