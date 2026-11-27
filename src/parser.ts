/**
 * 零依赖递归下降 GraphQL 解析器：将词法 Token 流解析为查询文档 AST。
 * 覆盖操作定义、变量定义、选择集、别名、参数、指令、具名/内联片段。
 */

import { Lexer } from './lexer.ts';
import type {
  ArgumentNode,
  DefinitionNode,
  DirectiveNode,
  DocumentNode,
  FieldNode,
  FragmentDefinitionNode,
  FragmentSpreadNode,
  InlineFragmentNode,
  ObjectFieldNode,
  OperationDefinitionNode,
  SelectionNode,
  SelectionSetNode,
  SourceLocation,
  Token,
  TypeNode,
  ValueNode,
  VariableDefinitionNode,
} from './types.ts';
import { TokenKind } from './types.ts';

export class ParseError extends Error {
  readonly line: number;
  readonly column: number;

  constructor(message: string, line: number, column: number) {
    super(`${message} (line ${line}, column ${column})`);
    this.name = 'ParseError';
    this.line = line;
    this.column = column;
  }
}

const PUNCT = {
  bang: '!',
  dollar: '$',
  lparen: '(',
  rparen: ')',
  ellipsis: '...',
  colon: ':',
  eq: '=',
  at: '@',
  lbracket: '[',
  rbracket: ']',
  lbrace: '{',
  rbrace: '}',
} as const;

const OPERATION_TYPES = new Set(['query', 'mutation', 'subscription']);

export class Parser {
  private readonly tokens: Token[];
  private index = 0;
  private nesting = 0;
  private readonly maxParseNesting: number;

  constructor(source: string, maxParseNesting = 200) {
    this.tokens = new Lexer(source).tokenize();
    this.maxParseNesting = maxParseNesting;
  }

  static parse(source: string, maxParseNesting = 200): DocumentNode {
    return new Parser(source, maxParseNesting).parseDocument();
  }

  private get current(): Token {
    return this.tokens[this.index];
  }

  private locFrom(start: Token): SourceLocation {
    const end = this.tokens[this.index - 1];
    return { start: start.start, end: end.end, line: start.line, column: start.column };
  }

  private expectPunctuator(value: string): Token {
    const token = this.current;
    if (token.kind !== TokenKind.PUNCTUATOR || token.value !== value) {
      throw new ParseError(
        `Expected "${value}" but found ${this.describe(token)}`,
        token.line,
        token.column,
      );
    }
    this.index += 1;
    return token;
  }

  private expectKeyword(value: string): Token {
    const token = this.current;
    if (token.kind !== TokenKind.NAME || token.value !== value) {
      throw new ParseError(
        `Expected keyword "${value}" but found ${this.describe(token)}`,
        token.line,
        token.column,
      );
    }
    this.index += 1;
    return token;
  }

  private expectName(): Token {
    const token = this.current;
    if (token.kind !== TokenKind.NAME) {
      throw new ParseError(
        `Expected a name but found ${this.describe(token)}`,
        token.line,
        token.column,
      );
    }
    this.index += 1;
    return token;
  }

  private optionalPunctuator(value: string): boolean {
    if (this.current.kind === TokenKind.PUNCTUATOR && this.current.value === value) {
      this.index += 1;
      return true;
    }
    return false;
  }

  private describe(token: Token): string {
    if (token.kind === TokenKind.EOF) {
      return 'end of input';
    }
    return `"${token.value}"`;
  }

  private enterNesting(start: Token): void {
    this.nesting += 1;
    if (this.nesting > this.maxParseNesting) {
      throw new ParseError(
        `Syntax nesting exceeds parser limit of ${this.maxParseNesting}`,
        start.line,
        start.column,
      );
    }
  }

  private exitNesting(): void {
    this.nesting -= 1;
  }

  parseDocument(): DocumentNode {
    const definitions: DefinitionNode[] = [];
    while (this.current.kind !== TokenKind.EOF) {
      definitions.push(this.parseDefinition());
    }
    return { kind: 'Document', definitions };
  }

  private parseDefinition(): DefinitionNode {
    if (
      this.current.kind === TokenKind.PUNCTUATOR &&
      this.current.value === PUNCT.lbrace
    ) {
      return this.parseOperationDefinition();
    }
    if (this.current.kind === TokenKind.NAME) {
      if (OPERATION_TYPES.has(this.current.value)) {
        return this.parseOperationDefinition();
      }
      if (this.current.value === 'fragment') {
        return this.parseFragmentDefinition();
      }
    }
    const token = this.current;
    throw new ParseError(
      `Expected operation or fragment definition but found ${this.describe(token)}`,
      token.line,
      token.column,
    );
  }

  private parseOperationDefinition(): OperationDefinitionNode {
    const start = this.current;
    this.enterNesting(start);
    let operation: OperationDefinitionNode['operation'] = 'query';
    let name: string | undefined;
    let variableDefinitions: VariableDefinitionNode[] = [];
    let directives: DirectiveNode[] = [];

    if (
      this.current.kind !== TokenKind.PUNCTUATOR ||
      this.current.value !== PUNCT.lbrace
    ) {
      const operationToken = this.expectName();
      if (!OPERATION_TYPES.has(operationToken.value)) {
        throw new ParseError(
          `Unknown operation type "${operationToken.value}"`,
          operationToken.line,
          operationToken.column,
        );
      }
      operation = operationToken.value as OperationDefinitionNode['operation'];
      if (this.current.kind === TokenKind.NAME) {
        name = this.expectName().value;
      }
      variableDefinitions = this.parseVariableDefinitions();
      directives = this.parseDirectives();
    }

    const selectionSet = this.parseSelectionSet();
    this.exitNesting();
    return {
      kind: 'OperationDefinition',
      operation,
      name,
      variableDefinitions,
      directives,
      selectionSet,
      loc: this.locFrom(start),
    };
  }

  private parseVariableDefinitions(): VariableDefinitionNode[] {
    if (!this.optionalPunctuator(PUNCT.lparen)) {
      return [];
    }
    const definitions: VariableDefinitionNode[] = [];
    while (!this.optionalPunctuator(PUNCT.rparen)) {
      definitions.push(this.parseVariableDefinition());
    }
    return definitions;
  }

  private parseVariableDefinition(): VariableDefinitionNode {
    const start = this.expectPunctuator(PUNCT.dollar);
    const variable = this.expectName().value;
    this.expectPunctuator(PUNCT.colon);
    const type = this.parseType();
    let defaultValue: ValueNode | undefined;
    if (this.optionalPunctuator(PUNCT.eq)) {
      defaultValue = this.parseValueLiteral(true);
    }
    const directives = this.parseDirectives();
    return { variable, type, defaultValue, directives, loc: this.locFrom(start) };
  }

  private parseType(): TypeNode {
    const start = this.current;
    let type: TypeNode;
    if (this.optionalPunctuator(PUNCT.lbracket)) {
      const inner = this.parseType();
      this.expectPunctuator(PUNCT.rbracket);
      type = { kind: 'ListType', type: inner, loc: this.locFrom(start) };
    } else {
      const name = this.expectName();
      type = { kind: 'NamedType', name: name.value, loc: this.locFrom(start) };
    }
    if (this.optionalPunctuator(PUNCT.bang)) {
      type = { kind: 'NonNullType', type, loc: this.locFrom(start) };
    }
    return type;
  }

  private parseFragmentDefinition(): FragmentDefinitionNode {
    const start = this.expectKeyword('fragment');
    this.enterNesting(start);
    const name = this.expectName().value;
    if (name === 'on') {
      throw new ParseError('Fragment cannot be named "on"', start.line, start.column);
    }
    this.expectKeyword('on');
    const typeCondition = this.expectName().value;
    const directives = this.parseDirectives();
    const selectionSet = this.parseSelectionSet();
    this.exitNesting();
    return {
      kind: 'FragmentDefinition',
      name,
      typeCondition,
      directives,
      selectionSet,
      loc: this.locFrom(start),
    };
  }

  private parseSelectionSet(): SelectionSetNode {
    const start = this.expectPunctuator(PUNCT.lbrace);
    this.enterNesting(start);
    const selections: SelectionNode[] = [];
    while (!this.optionalPunctuator(PUNCT.rbrace)) {
      if (this.current.kind === TokenKind.EOF) {
        throw new ParseError('Unterminated selection set', start.line, start.column);
      }
      selections.push(this.parseSelection());
    }
    this.exitNesting();
    return { kind: 'SelectionSet', selections, loc: this.locFrom(start) };
  }

  private parseSelection(): SelectionNode {
    if (
      this.current.kind === TokenKind.PUNCTUATOR &&
      this.current.value === PUNCT.ellipsis
    ) {
      return this.parseFragment();
    }
    return this.parseField();
  }

  private parseField(): FieldNode {
    const start = this.current;
    const first = this.expectName();
    let alias: string | undefined;
    let name = first.value;
    if (this.optionalPunctuator(PUNCT.colon)) {
      alias = first.value;
      name = this.expectName().value;
    }
    const args = this.parseArguments();
    const directives = this.parseDirectives();
    let selectionSet: SelectionSetNode | undefined;
    if (
      this.current.kind === TokenKind.PUNCTUATOR &&
      this.current.value === PUNCT.lbrace
    ) {
      selectionSet = this.parseSelectionSet();
    }
    return {
      kind: 'Field',
      alias,
      name,
      args,
      directives,
      selectionSet,
      loc: this.locFrom(start),
    };
  }

  private parseFragment(): FragmentSpreadNode | InlineFragmentNode {
    const start = this.expectPunctuator(PUNCT.ellipsis);
    if (this.current.kind === TokenKind.NAME && this.current.value !== 'on') {
      const name = this.expectName().value;
      const directives = this.parseDirectives();
      return {
        kind: 'FragmentSpread',
        name,
        directives,
        loc: this.locFrom(start),
      };
    }
    let typeCondition: string | undefined;
    if (this.current.kind === TokenKind.NAME && this.current.value === 'on') {
      this.index += 1;
      typeCondition = this.expectName().value;
    }
    const directives = this.parseDirectives();
    const selectionSet = this.parseSelectionSet();
    return {
      kind: 'InlineFragment',
      typeCondition,
      directives,
      selectionSet,
      loc: this.locFrom(start),
    };
  }

  private parseArguments(): ArgumentNode[] {
    if (!this.optionalPunctuator(PUNCT.lparen)) {
      return [];
    }
    const args: ArgumentNode[] = [];
    while (!this.optionalPunctuator(PUNCT.rparen)) {
      const start = this.current;
      const name = this.expectName().value;
      this.expectPunctuator(PUNCT.colon);
      const value = this.parseValueLiteral(false);
      args.push({ name, value, loc: this.locFrom(start) });
    }
    return args;
  }

  private parseDirectives(): DirectiveNode[] {
    const directives: DirectiveNode[] = [];
    while (this.optionalPunctuator(PUNCT.at)) {
      const start = this.tokens[this.index - 1];
      const name = this.expectName().value;
      const args = this.parseArguments();
      directives.push({ name, args, loc: this.locFrom(start) });
    }
    return directives;
  }

  private parseValueLiteral(isConst: boolean): ValueNode {
    const token = this.current;
    if (token.kind === TokenKind.PUNCTUATOR && token.value === PUNCT.dollar) {
      if (isConst) {
        throw new ParseError(
          'Variable references are not allowed in const positions',
          token.line,
          token.column,
        );
      }
      this.index += 1;
      const name = this.expectName();
      return { kind: 'Variable', name: name.value, loc: this.locFrom(token) };
    }

    if (token.kind === TokenKind.INT) {
      this.index += 1;
      return { kind: 'IntValue', value: token.value, loc: this.locFrom(token) };
    }
    if (token.kind === TokenKind.FLOAT) {
      this.index += 1;
      return { kind: 'FloatValue', value: token.value, loc: this.locFrom(token) };
    }
    if (token.kind === TokenKind.STRING || token.kind === TokenKind.BLOCK_STRING) {
      this.index += 1;
      return { kind: 'StringValue', value: token.value, loc: this.locFrom(token) };
    }
    if (token.kind === TokenKind.NAME) {
      this.index += 1;
      if (token.value === 'true' || token.value === 'false') {
        return {
          kind: 'BooleanValue',
          value: token.value === 'true',
          loc: this.locFrom(token),
        };
      }
      if (token.value === 'null') {
        return { kind: 'NullValue', loc: this.locFrom(token) };
      }
      return { kind: 'EnumValue', value: token.value, loc: this.locFrom(token) };
    }
    if (token.kind === TokenKind.PUNCTUATOR && token.value === PUNCT.lbracket) {
      this.index += 1;
      const values: ValueNode[] = [];
      while (!this.optionalPunctuator(PUNCT.rbracket)) {
        values.push(this.parseValueLiteral(isConst));
      }
      return { kind: 'ListValue', values, loc: this.locFrom(token) };
    }
    if (token.kind === TokenKind.PUNCTUATOR && token.value === PUNCT.lbrace) {
      this.index += 1;
      const fields: ObjectFieldNode[] = [];
      while (!this.optionalPunctuator(PUNCT.rbrace)) {
        const fieldStart = this.current;
        const fieldName = this.expectName().value;
        this.expectPunctuator(PUNCT.colon);
        const value = this.parseValueLiteral(isConst);
        fields.push({ name: fieldName, value, loc: this.locFrom(fieldStart) });
      }
      return { kind: 'ObjectValue', fields, loc: this.locFrom(token) };
    }

    throw new ParseError(
      `Unexpected ${this.describe(token)} while parsing value`,
      token.line,
      token.column,
    );
  }
}
