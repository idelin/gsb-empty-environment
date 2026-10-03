/**
 * Hand-written lexer + recursive-descent parser producing the positioned AST
 * consumed by the attribute grammar in language.ts.
 *
 * Grammar:
 *   program    := stmt* EOF
 *   stmt       := 'let' IDENT (':' typeName)? '=' expr ';'
 *               | 'print' expr ';'
 *               | 'if' expr block ('else' block)?
 *   block      := '{' stmt* '}'
 *   typeName   := 'int' | 'bool'
 *   expr       := equality
 *   equality   := relational (('==' | '!=') relational)*
 *   relational := additive (('<' | '>' | '<=' | '>=') additive)*
 *   additive   := term (('+' | '-') term)*
 *   term       := factor (('*' | '/') factor)*
 *   factor     := NUMBER | 'true' | 'false' | IDENT | '(' expr ')'
 */

import type { ASTNode, Pos } from './grammar.ts';
import { LangError } from './errors.ts';

type TokenType =
  | 'let' | 'print' | 'if' | 'else' | 'true' | 'false' | 'int' | 'bool'
  | 'IDENT' | 'NUMBER'
  | 'OP' | 'ASSIGN' | 'LPAREN' | 'RPAREN' | 'LBRACE' | 'RBRACE' | 'SEMI' | 'COLON'
  | 'EOF';

interface Token {
  type: TokenType;
  text: string;
  pos: Pos;
}

const KEYWORDS: Record<string, TokenType> = {
  let: 'let', print: 'print', if: 'if', else: 'else',
  true: 'true', false: 'false', int: 'int', bool: 'bool',
};

const OPERATORS = ['==', '!=', '<=', '>=', '+', '-', '*', '/', '<', '>'];

export function lex(source: string): Token[] {
  const tokens: Token[] = [];
  let offset = 0;
  let line = 1;
  let lineStart = 0;

  const pos = (): Pos => ({ line, col: offset - lineStart + 1, offset });

  while (offset < source.length) {
    const ch = source[offset];

    if (ch === '\n') { line++; offset++; lineStart = offset; continue; }
    if (ch === ' ' || ch === '\t' || ch === '\r') { offset++; continue; }
    if (ch === '/' && source[offset + 1] === '/') {
      while (offset < source.length && source[offset] !== '\n') offset++;
      continue;
    }

    const here = pos();

    if (/[0-9]/.test(ch)) {
      let end = offset;
      while (end < source.length && /[0-9]/.test(source[end])) end++;
      tokens.push({ type: 'NUMBER', text: source.slice(offset, end), pos: here });
      offset = end;
      continue;
    }

    if (/[A-Za-z_]/.test(ch)) {
      let end = offset;
      while (end < source.length && /[A-Za-z0-9_]/.test(source[end])) end++;
      const text = source.slice(offset, end);
      tokens.push({ type: KEYWORDS[text] ?? 'IDENT', text, pos: here });
      offset = end;
      continue;
    }

    const two = source.slice(offset, offset + 2);
    if (OPERATORS.includes(two)) {
      tokens.push({ type: 'OP', text: two, pos: here });
      offset += 2;
      continue;
    }
    if (OPERATORS.includes(ch)) {
      tokens.push({ type: 'OP', text: ch, pos: here });
      offset += 1;
      continue;
    }

    switch (ch) {
      case '=': tokens.push({ type: 'ASSIGN', text: ch, pos: here }); break;
      case '(': tokens.push({ type: 'LPAREN', text: ch, pos: here }); break;
      case ')': tokens.push({ type: 'RPAREN', text: ch, pos: here }); break;
      case '{': tokens.push({ type: 'LBRACE', text: ch, pos: here }); break;
      case '}': tokens.push({ type: 'RBRACE', text: ch, pos: here }); break;
      case ';': tokens.push({ type: 'SEMI', text: ch, pos: here }); break;
      case ':': tokens.push({ type: 'COLON', text: ch, pos: here }); break;
      default:
        throw new LangError(`unexpected character '${ch}'`, here);
    }
    offset++;
  }

  tokens.push({ type: 'EOF', text: '<eof>', pos: pos() });
  return tokens;
}

class Parser {
  private index = 0;
  private readonly tokens: Token[];

  constructor(tokens: Token[]) {
    this.tokens = tokens;
  }

  private peek(): Token {
    return this.tokens[this.index];
  }

  private next(): Token {
    return this.tokens[this.index++];
  }

  private expect(type: TokenType, what: string): Token {
    const token = this.peek();
    if (token.type !== type) {
      throw new LangError(`expected ${what}, found '${token.text}'`, token.pos);
    }
    return this.next();
  }

  parseProgram(): ASTNode {
    const start = this.peek().pos;
    const stmts: ASTNode[] = [];
    while (this.peek().type !== 'EOF') {
      stmts.push(this.parseStmt());
    }
    return { kind: 'Program', pos: start, stmts };
  }

  private parseStmt(): ASTNode {
    const token = this.peek();
    switch (token.type) {
      case 'let': return this.parseVarDecl();
      case 'print': return this.parsePrint();
      case 'if': return this.parseIf();
      default:
        throw new LangError(`expected a statement, found '${token.text}'`, token.pos);
    }
  }

  private parseVarDecl(): ASTNode {
    const letToken = this.next(); // 'let'
    const name = this.expect('IDENT', 'a variable name');
    let annotation: string | undefined;
    if (this.peek().type === 'COLON') {
      this.next();
      const typeToken = this.peek();
      if (typeToken.type !== 'int' && typeToken.type !== 'bool') {
        throw new LangError(`expected a type name ('int' or 'bool'), found '${typeToken.text}'`, typeToken.pos);
      }
      annotation = this.next().text;
    }
    this.expect('ASSIGN', "'='");
    const init = this.parseExpr();
    this.expect('SEMI', "';'");
    return { kind: 'VarDecl', pos: letToken.pos, name: name.text, annotation, init };
  }

  private parsePrint(): ASTNode {
    const printToken = this.next(); // 'print'
    const expr = this.parseExpr();
    this.expect('SEMI', "';'");
    return { kind: 'Print', pos: printToken.pos, expr };
  }

  private parseIf(): ASTNode {
    const ifToken = this.next(); // 'if'
    const cond = this.parseExpr();
    const thenBlock = this.parseBlock();
    let elseBlock: ASTNode | undefined;
    if (this.peek().type === 'else') {
      this.next();
      elseBlock = this.parseBlock();
    }
    return { kind: 'If', pos: ifToken.pos, cond, then: thenBlock, else: elseBlock };
  }

  private parseBlock(): ASTNode {
    const open = this.expect('LBRACE', "'{'");
    const stmts: ASTNode[] = [];
    while (this.peek().type !== 'RBRACE') {
      stmts.push(this.parseStmt());
    }
    this.next(); // '}'
    return { kind: 'Block', pos: open.pos, stmts };
  }

  private parseExpr(): ASTNode {
    return this.parseEquality();
  }

  private parseBinary(nextLevel: () => ASTNode, ops: string[]): () => ASTNode {
    return () => {
      let left = nextLevel();
      while (this.peek().type === 'OP' && ops.includes(this.peek().text)) {
        const opToken = this.next();
        const right = nextLevel();
        left = { kind: 'BinOp', pos: opToken.pos, op: opToken.text, left, right };
      }
      return left;
    };
  }

  private parseEquality = (): ASTNode =>
    this.parseBinary(() => this.parseRelational(), ['==', '!='])();

  private parseRelational = (): ASTNode =>
    this.parseBinary(() => this.parseAdditive(), ['<', '>', '<=', '>='])();

  private parseAdditive = (): ASTNode =>
    this.parseBinary(() => this.parseTerm(), ['+', '-'])();

  private parseTerm = (): ASTNode =>
    this.parseBinary(() => this.parseFactor(), ['*', '/'])();

  private parseFactor(): ASTNode {
    const token = this.peek();
    switch (token.type) {
      case 'NUMBER':
        this.next();
        return { kind: 'IntLit', pos: token.pos, value: Number.parseInt(token.text, 10) };
      case 'true':
        this.next();
        return { kind: 'BoolLit', pos: token.pos, value: true };
      case 'false':
        this.next();
        return { kind: 'BoolLit', pos: token.pos, value: false };
      case 'IDENT':
        this.next();
        return { kind: 'VarRef', pos: token.pos, name: token.text };
      case 'LPAREN': {
        this.next();
        const expr = this.parseExpr();
        this.expect('RPAREN', "')'");
        return expr;
      }
      default:
        throw new LangError(`expected an expression, found '${token.text}'`, token.pos);
    }
  }
}

export function parse(source: string): ASTNode {
  return new Parser(lex(source)).parseProgram();
}
