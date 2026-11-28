import { tokenize, Token } from './lexer.ts';
import {
  Node,
  IdentNode,
  SelectNode,
  IndexNode,
  CallNode,
  LiteralNode,
  ListNode,
  MapNode,
  UnaryNode,
  BinaryNode,
  LogicalNode,
  ConditionalNode,
  HasNode,
  MacroNode,
  MacroName,
} from './ast.ts';

export class ParseError extends Error {
  constructor(message: string, readonly pos: number) {
    super(`CEL parse error at ${pos}: ${message}`);
    this.name = 'ParseError';
  }
}

const BINARY_PRECEDENCE: Record<string, number> = {
  '||': 1,
  '&&': 2,
  '==': 3,
  '!=': 3,
  in: 3,
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

const MACRO_NAMES = new Set<MacroName>([
  'all',
  'exists',
  'exists_one',
  'filter',
  'map',
  'has',
  'size',
]);

export function parse(source: string): Node {
  const tokens = tokenize(source);
  const p = new Parser(tokens);
  const expr = p.parseExpression(0);
  p.expect('EOF');
  return expr;
}

class Parser {
  private idx = 0;

  constructor(private readonly tokens: Token[]) {}

  private peek(): Token {
    return this.tokens[this.idx];
  }

  private next(): Token {
    const t = this.tokens[this.idx];
    this.idx += 1;
    return t;
  }

  expect(op: string): Token {
    const t = this.peek();
    const matches = op === 'EOF' ? t.type === 'EOF' : t.type === 'OP' && t.value === op;
    if (!matches) {
      throw new ParseError(`expected ${JSON.stringify(op)} but got ${JSON.stringify(t.value)}`, t.pos);
    }
    return this.next();
  }

  matchOp(op: string): boolean {
    const t = this.peek();
    if (t.type === 'OP' && t.value === op) {
      this.next();
      return true;
    }
    return false;
  }

  parseExpression(minPrec: number): Node {
    let left = this.parseUnary();

    for (;;) {
      const t = this.peek();
      if (t.type === 'OP' && t.value === '?') {
        if (minPrec > 1) return left;
        this.next();
        const consequent = this.parseExpression(1);
        this.expect(':');
        const alternate = this.parseExpression(1);
        left = { kind: 'conditional', test: left, consequent, alternate } as ConditionalNode;
        continue;
      }
      if (t.type !== 'OP') return left;
      const prec = BINARY_PRECEDENCE[t.value];
      if (prec === undefined || prec < minPrec) return left;
      this.next();
      const right = this.parseExpression(prec + 1);
      if (t.value === '&&' || t.value === '||') {
        left = { kind: 'logical', op: t.value, left, right } as LogicalNode;
      } else if (t.value === 'in') {
        left = { kind: 'in', left, right };
      } else {
        left = { kind: 'binary', op: t.value as BinaryNode['op'], left, right } as BinaryNode;
      }
    }
  }

  private parseUnary(): Node {
    const t = this.peek();
    if (t.type === 'OP' && (t.value === '!' || t.value === '-' || t.value === '+')) {
      this.next();
      const operand = this.parseUnary();
      return { kind: 'unary', op: t.value, operand } as UnaryNode;
    }
    return this.parsePostfix();
  }

  private parsePostfix(): Node {
    let node = this.parsePrimary();

    for (;;) {
      const t = this.peek();
      if (t.type === 'OP' && t.value === '.') {
        this.next();
        const field = this.expectIdent();
        // has(x.f) macro
        if (field === 'has' && this.peek().value === '(') {
          this.next();
          const inner = this.parseExpression(0);
          this.expect(')');
          if (inner.kind !== 'select' && inner.kind !== 'index') {
            throw new ParseError('has() requires a field access expression', t.pos);
          }
          node = { kind: 'has', operand: inner } as HasNode;
          continue;
        }
        // receiver-style macros: e.all(v, ...), e.exists(...), e.filter(...), e.map(...), e.size()
        if (this.peek().value === '(' && MACRO_NAMES.has(field as MacroName)) {
          this.next();
          node = this.parseMacroArgs(field as MacroName, node, t.pos);
          continue;
        }
        if (this.peek().value === '(') {
          this.next();
          const args = this.parseArgList();
          node = { kind: 'call', receiver: node, name: field, args } as CallNode;
          continue;
        }
        node = { kind: 'select', operand: node, field } as SelectNode;
        continue;
      }
      if (t.type === 'OP' && t.value === '[') {
        this.next();
        const index = this.parseExpression(0);
        this.expect(']');
        node = { kind: 'index', operand: node, index } as IndexNode;
        continue;
      }
      if (t.type === 'OP' && t.value === '(' && node.kind === 'ident') {
        if ((node as IdentNode).name === 'has') {
          this.next();
          const inner = this.parseExpression(0);
          this.expect(')');
          if (inner.kind !== 'select' && inner.kind !== 'index') {
            throw new ParseError('has() requires a field access expression', t.pos);
          }
          node = { kind: 'has', operand: inner } as HasNode;
          continue;
        }
        // Global-style macros: size(x)
        if ((node as IdentNode).name === 'size') {
          this.next();
          const args = this.parseArgList();
          if (args.length !== 1) {
            throw new ParseError('size() expects exactly one argument', t.pos);
          }
          node = {
            kind: 'macro',
            name: 'size',
            receiver: args[0],
            bindVar: null,
            predicate: null,
          } as MacroNode;
          continue;
        }
        this.next();
        const args = this.parseArgList();
        node = {
          kind: 'call',
          receiver: null,
          name: (node as IdentNode).name,
          args,
        } as CallNode;
        continue;
      }
      return node;
    }
  }

  private parseMacroArgs(name: MacroName, receiver: Node, pos: number): Node {
    if (name === 'has') {
      const inner = this.parseExpression(0);
      this.expect(')');
      if (inner.kind !== 'select' && inner.kind !== 'index') {
        throw new ParseError('has() requires a field access expression', pos);
      }
      return { kind: 'has', operand: inner } as HasNode;
    }
    if (name === 'size') {
      this.expect(')');
      return { kind: 'macro', name: 'size', receiver, bindVar: null, predicate: null } as MacroNode;
    }
    // all/exists/exists_one/filter/map: e.<macro>(var, predicate)
    const bindToken = this.peek();
    if (bindToken.type !== 'IDENT') {
      throw new ParseError(`macro ${name} expects a bound variable`, bindToken.pos);
    }
    const bindVar = this.next().value;
    this.expect(',');
    const predicate = this.parseExpression(0);
    this.expect(')');
    return { kind: 'macro', name, receiver, bindVar, predicate } as MacroNode;
  }

  private parseArgList(): Node[] {
    const args: Node[] = [];
    if (this.peek().value === ')') {
      this.next();
      return args;
    }
    args.push(this.parseExpression(0));
    while (this.matchOp(',')) {
      if (this.peek().value === ')') break; // tolerate trailing comma
      args.push(this.parseExpression(0));
    }
    this.expect(')');
    return args;
  }

  private expectIdent(): string {
    const t = this.peek();
    if (t.type !== 'IDENT') {
      throw new ParseError(`expected identifier but got ${JSON.stringify(t.value)}`, t.pos);
    }
    return this.next().value;
  }

  private parsePrimary(): Node {
    const t = this.peek();

    if (t.type === 'INT' || t.type === 'UINT' || t.type === 'DOUBLE') {
      this.next();
      return {
        kind: 'literal',
        type: t.type === 'UINT' ? 'uint' : t.type === 'DOUBLE' ? 'double' : 'int',
        raw: t.value,
      } as LiteralNode;
    }
    if (t.type === 'STRING' || t.type === 'BYTES') {
      this.next();
      return { kind: 'literal', type: t.type === 'BYTES' ? 'bytes' : 'string', raw: t.value } as LiteralNode;
    }
    if (t.type === 'IDENT') {
      this.next();
      return { kind: 'ident', name: t.value } as IdentNode;
    }
    if (t.type === 'OP' && t.value === 'true') {
      this.next();
      return { kind: 'literal', type: 'bool', raw: 'true' } as LiteralNode;
    }
    if (t.type === 'OP' && t.value === 'false') {
      this.next();
      return { kind: 'literal', type: 'bool', raw: 'false' } as LiteralNode;
    }
    if (t.type === 'OP' && t.value === 'null') {
      this.next();
      return { kind: 'literal', type: 'null', raw: 'null' } as LiteralNode;
    }
    if (t.type === 'OP' && t.value === '(') {
      this.next();
      const expr = this.parseExpression(0);
      this.expect(')');
      return expr;
    }
    if (t.type === 'OP' && t.value === '[') {
      this.next();
      const elements: Node[] = [];
      if (this.peek().value !== ']') {
        elements.push(this.parseExpression(0));
        while (this.matchOp(',')) {
          if (this.peek().value === ']') break;
          elements.push(this.parseExpression(0));
        }
      }
      this.expect(']');
      return { kind: 'list', elements } as ListNode;
    }
    if (t.type === 'OP' && t.value === '{') {
      this.next();
      const entries: MapNode['entries'] = [];
      if (this.peek().value !== '}') {
        entries.push(this.parseMapEntry());
        while (this.matchOp(',')) {
          if (this.peek().value === '}') break;
          entries.push(this.parseMapEntry());
        }
      }
      this.expect('}');
      return { kind: 'map', entries } as MapNode;
    }

    throw new ParseError(`unexpected token ${JSON.stringify(t.value)}`, t.pos);
  }

  private parseMapEntry(): MapNode['entries'][number] {
    const keyToken = this.next();
    let key: Node;
    if (keyToken.type === 'STRING') {
      key = { kind: 'literal', type: 'string', raw: keyToken.value } as LiteralNode;
    } else if (keyToken.type === 'IDENT') {
      key = { kind: 'literal', type: 'string', raw: keyToken.value } as LiteralNode;
    } else {
      throw new ParseError('map key must be a string or identifier', keyToken.pos);
    }
    this.expect(':');
    const value = this.parseExpression(0);
    return { key, value };
  }
}
