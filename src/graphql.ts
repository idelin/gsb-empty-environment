export interface SourceLocation {
  start: number;
  end: number;
  line: number;
  column: number;
}

export interface VariableDefinitionNode {
  kind: "VariableDefinition";
  name: string;
  type: string;
  defaultValue: ValueNode | null;
  directives: DirectiveNode[];
  loc: SourceLocation;
}

export interface DirectiveNode {
  kind: "Directive";
  name: string;
  arguments: ArgumentNode[];
  loc: SourceLocation;
}

export interface ArgumentNode {
  kind: "Argument";
  name: string;
  value: ValueNode;
  loc: SourceLocation;
}

export type ValueNode =
  | VariableValueNode
  | IntValueNode
  | FloatValueNode
  | StringValueNode
  | BooleanValueNode
  | NullValueNode
  | EnumValueNode
  | ListValueNode
  | ObjectValueNode;

export interface VariableValueNode {
  kind: "Variable";
  name: string;
  loc: SourceLocation;
}

export interface IntValueNode {
  kind: "IntValue";
  value: number;
  raw: string;
  loc: SourceLocation;
}

export interface FloatValueNode {
  kind: "FloatValue";
  value: number;
  raw: string;
  loc: SourceLocation;
}

export interface StringValueNode {
  kind: "StringValue";
  value: string;
  loc: SourceLocation;
}

export interface BooleanValueNode {
  kind: "BooleanValue";
  value: boolean;
  loc: SourceLocation;
}

export interface NullValueNode {
  kind: "NullValue";
  loc: SourceLocation;
}

export interface EnumValueNode {
  kind: "EnumValue";
  value: string;
  loc: SourceLocation;
}

export interface ListValueNode {
  kind: "ListValue";
  values: ValueNode[];
  loc: SourceLocation;
}

export interface ObjectValueNode {
  kind: "ObjectValue";
  fields: ArgumentNode[];
  loc: SourceLocation;
}

export interface FieldNode {
  kind: "Field";
  name: string;
  alias: string | null;
  arguments: ArgumentNode[];
  directives: DirectiveNode[];
  selectionSet: SelectionSetNode | null;
  loc: SourceLocation;
}

export interface FragmentSpreadNode {
  kind: "FragmentSpread";
  name: string;
  directives: DirectiveNode[];
  loc: SourceLocation;
}

export interface InlineFragmentNode {
  kind: "InlineFragment";
  typeCondition: string | null;
  directives: DirectiveNode[];
  selectionSet: SelectionSetNode;
  loc: SourceLocation;
}

export interface SelectionSetNode {
  kind: "SelectionSet";
  selections: SelectionNode[];
  loc: SourceLocation;
}

export type SelectionNode = FieldNode | FragmentSpreadNode | InlineFragmentNode;

export interface OperationDefinitionNode {
  kind: "OperationDefinition";
  operation: "query" | "mutation" | "subscription";
  name: string | null;
  variableDefinitions: VariableDefinitionNode[];
  directives: DirectiveNode[];
  selectionSet: SelectionSetNode;
  loc: SourceLocation;
}

export interface FragmentDefinitionNode {
  kind: "FragmentDefinition";
  name: string;
  typeCondition: string;
  directives: DirectiveNode[];
  selectionSet: SelectionSetNode;
  loc: SourceLocation;
}

export type DefinitionNode = OperationDefinitionNode | FragmentDefinitionNode;

export interface DocumentNode {
  kind: "Document";
  definitions: DefinitionNode[];
  loc: SourceLocation;
}

interface TokenLocation {
  line: number;
  column: number;
  index: number;
}

interface Token extends TokenLocation {
  kind: "Name" | "Int" | "Float" | "String" | "Punct" | "EOF";
  value: string;
  end: number;
}

export class GraphQLSyntaxError extends Error {
  readonly loc: TokenLocation;

  constructor(message: string, loc: TokenLocation) {
    super(`${message} (line ${loc.line}, column ${loc.column})`);
    this.name = "GraphQLSyntaxError";
    this.loc = loc;
  }
}

const PUNCTUATORS = new Set([
  "!",
  "$",
  "&",
  "(",
  ")",
  ":",
  "=",
  "@",
  "[",
  "]",
  "{",
  "|",
  "}",
]);

class Lexer {
  private readonly source: string;
  private index = 0;
  private line = 1;
  private column = 1;

  constructor(source: string) {
    this.source = source;
  }

  tokenize(): Token[] {
    const tokens: Token[] = [];
    while (true) {
      this.skipIgnored();
      if (this.index >= this.source.length) {
        tokens.push({
          kind: "EOF",
          value: "",
          start: this.index,
          end: this.index,
          line: this.line,
          column: this.column,
          index: this.index,
        });
        return tokens;
      }

      const char = this.source[this.index];
      const location = this.location();

      if (char === ".") {
        if (this.source.slice(this.index, this.index + 3) !== "...") {
          throw new GraphQLSyntaxError("Expected spread operator '...'", location);
        }
        this.advance(3);
        tokens.push(this.token("Punct", "...", location));
        continue;
      }

      if (PUNCTUATORS.has(char)) {
        this.advance();
        tokens.push(this.token("Punct", char, location));
        continue;
      }

      if (char === '"') {
        tokens.push(this.readString(location));
        continue;
      }

      if (char === "-" || (char >= "0" && char <= "9")) {
        tokens.push(this.readNumber(location));
        continue;
      }

      if (isNameStart(char)) {
        tokens.push(this.readName(location));
        continue;
      }

      throw new GraphQLSyntaxError(`Unexpected character '${char}'`, location);
    }
  }

  private skipIgnored(): void {
    while (this.index < this.source.length) {
      const char = this.source[this.index];

      if (char === "\uFEFF" || char === "," || char === " " || char === "\t") {
        this.advance();
        continue;
      }

      if (char === "\r") {
        this.advance();
        if (this.source[this.index] === "\n") {
          this.advance();
        }
        continue;
      }

      if (char === "\n") {
        this.advance();
        continue;
      }

      if (char === "#") {
        while (
          this.index < this.source.length &&
          this.source[this.index] !== "\n" &&
          this.source[this.index] !== "\r"
        ) {
          this.advance();
        }
        continue;
      }

      return;
    }
  }

  private readString(location: TokenLocation): Token {
    if (this.source.slice(this.index, this.index + 3) === '"""') {
      return this.readBlockString(location);
    }

    this.advance();
    let value = "";
    while (this.index < this.source.length) {
      const char = this.source[this.index];
      if (char === '"') {
        this.advance();
        return this.token("String", value, location);
      }

      if (char === "\n" || char === "\r") {
        throw new GraphQLSyntaxError("Unterminated string", location);
      }

      if (char !== "\\") {
        value += char;
        this.advance();
        continue;
      }

      this.advance();
      const escape = this.source[this.index];
      if (escape === undefined) {
        throw new GraphQLSyntaxError("Unterminated string escape", location);
      }

      const escaped: Record<string, string> = {
        '"': '"',
        "\\": "\\",
        "/": "/",
        b: "\b",
        f: "\f",
        n: "\n",
        r: "\r",
        t: "\t",
      };

      if (escape in escaped) {
        value += escaped[escape];
        this.advance();
        continue;
      }

      if (escape === "u") {
        this.advance();
        const hex = this.source.slice(this.index, this.index + 4);
        if (!/^[0-9a-fA-F]{4}$/.test(hex)) {
          throw new GraphQLSyntaxError("Invalid Unicode escape", location);
        }
        value += String.fromCharCode(Number.parseInt(hex, 16));
        this.advance(4);
        continue;
      }

      throw new GraphQLSyntaxError(`Invalid string escape '\\${escape}'`, location);
    }

    throw new GraphQLSyntaxError("Unterminated string", location);
  }

  private readBlockString(location: TokenLocation): Token {
    this.advance(3);
    let raw = "";
    while (this.index < this.source.length) {
      if (this.source.slice(this.index, this.index + 3) === '"""') {
        this.advance(3);
        return this.token("String", blockStringValue(raw), location);
      }

      if (this.source.slice(this.index, this.index + 4) === '\\"""') {
        raw += '"""';
        this.advance(4);
        continue;
      }

      raw += this.source[this.index];
      this.advance();
    }

    throw new GraphQLSyntaxError("Unterminated block string", location);
  }

  private readNumber(location: TokenLocation): Token {
    const start = this.index;
    if (this.source[this.index] === "-") {
      this.advance();
    }

    if (this.source[this.index] === "0") {
      this.advance();
    } else {
      const firstDigit = this.source[this.index];
      if (firstDigit === undefined || firstDigit < "1" || firstDigit > "9") {
        throw new GraphQLSyntaxError("Invalid number", location);
      }
      while (isDigit(this.source[this.index])) {
        this.advance();
      }
    }

    let kind: "Int" | "Float" = "Int";
    if (this.source[this.index] === ".") {
      kind = "Float";
      this.advance();
      if (!isDigit(this.source[this.index])) {
        throw new GraphQLSyntaxError("Invalid float", location);
      }
      while (isDigit(this.source[this.index])) {
        this.advance();
      }
    }

    const exponent = this.source[this.index];
    if (exponent === "e" || exponent === "E") {
      kind = "Float";
      this.advance();
      const sign = this.source[this.index];
      if (sign === "+" || sign === "-") {
        this.advance();
      }
      if (!isDigit(this.source[this.index])) {
        throw new GraphQLSyntaxError("Invalid exponent", location);
      }
      while (isDigit(this.source[this.index])) {
        this.advance();
      }
    }

    return this.token(kind, this.source.slice(start, this.index), location);
  }

  private readName(location: TokenLocation): Token {
    const start = this.index;
    while (isNameContinue(this.source[this.index])) {
      this.advance();
    }
    return this.token("Name", this.source.slice(start, this.index), location);
  }

  private token(
    kind: Token["kind"],
    value: string,
    location: TokenLocation,
  ): Token {
    return {
      ...location,
      kind,
      value,
      end: this.index,
    };
  }

  private location(): TokenLocation {
    return {
      line: this.line,
      column: this.column,
      index: this.index,
    };
  }

  private advance(count = 1): void {
    for (let offset = 0; offset < count; offset += 1) {
      const char = this.source[this.index];
      if (char === undefined) {
        return;
      }
      this.index += 1;
      if (char === "\n") {
        this.line += 1;
        this.column = 1;
      } else {
        this.column += 1;
      }
    }
  }
}

class Parser {
  private readonly tokens: Token[];
  private index = 0;

  constructor(source: string) {
    this.tokens = new Lexer(source).tokenize();
  }

  parseDocument(): DocumentNode {
    const first = this.peek();
    const definitions: DefinitionNode[] = [];
    while (this.peek().kind !== "EOF") {
      definitions.push(this.parseDefinition());
    }
    if (definitions.length === 0) {
      throw this.error(this.peek(), "Expected an executable definition");
    }
    return {
      kind: "Document",
      definitions,
      loc: this.loc(first),
    };
  }

  private parseDefinition(): DefinitionNode {
    if (this.peekPunct("{")) {
      return this.parseOperation(null);
    }

    const token = this.expectKind("Name", "Expected a definition");
    if (token.value === "fragment") {
      return this.parseFragment();
    }

    if (
      token.value === "query" ||
      token.value === "mutation" ||
      token.value === "subscription"
    ) {
      return this.parseOperation(token);
    }

    throw this.error(token, `Unsupported definition '${token.value}'`);
  }

  private parseOperation(
    operationToken: Token | null,
  ): OperationDefinitionNode {
    const start = operationToken ?? this.peek();
    const operation =
      operationToken === null
        ? "query"
        : (operationToken.value as OperationDefinitionNode["operation"]);
    let name: string | null = null;
    if (this.peek().kind === "Name") {
      name = this.consume().value;
    }

    const variableDefinitions: VariableDefinitionNode[] = [];
    if (this.peekPunct("(")) {
      this.consume();
      while (!this.peekPunct(")")) {
        variableDefinitions.push(this.parseVariableDefinition());
      }
      this.expectPunct(")");
    }

    const directives = this.parseDirectives();
    const selectionSet = this.parseSelectionSet();
    return {
      kind: "OperationDefinition",
      operation,
      name,
      variableDefinitions,
      directives,
      selectionSet,
      loc: this.loc(start),
    };
  }

  private parseVariableDefinition(): VariableDefinitionNode {
    const start = this.expectPunct("$");
    const name = this.expectKind("Name", "Expected a variable name").value;
    this.expectPunct(":");
    const type = this.parseTypeReference();
    let defaultValue: ValueNode | null = null;
    if (this.peekPunct("=")) {
      this.consume();
      defaultValue = this.parseValue();
    }
    const directives = this.parseDirectives();
    return {
      kind: "VariableDefinition",
      name,
      type,
      defaultValue,
      directives,
      loc: this.loc(start),
    };
  }

  private parseTypeReference(): string {
    let type: string;
    if (this.peekPunct("[")) {
      this.consume();
      type = `[${this.parseTypeReference()}]`;
      this.expectPunct("]");
    } else {
      type = this.expectKind("Name", "Expected a type name").value;
    }

    if (this.peekPunct("!")) {
      this.consume();
      type += "!";
    }
    return type;
  }

  private parseFragment(): FragmentDefinitionNode {
    const start = this.previous();
    const name = this.expectKind("Name", "Expected a fragment name").value;
    const on = this.expectKind("Name", "Expected 'on' in fragment definition");
    if (on.value !== "on") {
      throw this.error(on, "Expected 'on' in fragment definition");
    }
    const typeCondition = this.expectKind(
      "Name",
      "Expected a fragment type condition",
    ).value;
    const directives = this.parseDirectives();
    const selectionSet = this.parseSelectionSet();
    return {
      kind: "FragmentDefinition",
      name,
      typeCondition,
      directives,
      selectionSet,
      loc: this.loc(start),
    };
  }

  private parseSelectionSet(): SelectionSetNode {
    const start = this.expectPunct("{");
    const selections: SelectionNode[] = [];
    while (!this.peekPunct("}")) {
      if (this.peek().kind === "EOF") {
        throw this.error(this.peek(), "Unterminated selection set");
      }
      selections.push(this.parseSelection());
    }
    this.expectPunct("}");
    if (selections.length === 0) {
      throw this.error(start, "Selection set cannot be empty");
    }
    return {
      kind: "SelectionSet",
      selections,
      loc: this.loc(start),
    };
  }

  private parseSelection(): SelectionNode {
    if (this.peekPunct("...")) {
      return this.parseSpread();
    }
    return this.parseField();
  }

  private parseSpread(): FragmentSpreadNode | InlineFragmentNode {
    const start = this.expectPunct("...");

    if (this.peekName("on")) {
      this.consume();
      const typeCondition = this.expectKind(
        "Name",
        "Expected an inline fragment type condition",
      ).value;
      const directives = this.parseDirectives();
      const selectionSet = this.parseSelectionSet();
      return {
        kind: "InlineFragment",
        typeCondition,
        directives,
        selectionSet,
        loc: this.loc(start),
      };
    }

    if (this.peekPunct("{") || this.peekPunct("@")) {
      const directives = this.parseDirectives();
      const selectionSet = this.parseSelectionSet();
      return {
        kind: "InlineFragment",
        typeCondition: null,
        directives,
        selectionSet,
        loc: this.loc(start),
      };
    }

    const name = this.expectKind("Name", "Expected a fragment name").value;
    const directives = this.parseDirectives();
    return {
      kind: "FragmentSpread",
      name,
      directives,
      loc: this.loc(start),
    };
  }

  private parseField(): FieldNode {
    const start = this.expectKind("Name", "Expected a field name");
    let alias: string | null = null;
    let name = start.value;
    if (this.peekPunct(":")) {
      this.consume();
      alias = name;
      name = this.expectKind("Name", "Expected a field name after alias").value;
    }

    const args = this.parseArguments();
    const directives = this.parseDirectives();
    const selectionSet = this.peekPunct("{") ? this.parseSelectionSet() : null;
    return {
      kind: "Field",
      name,
      alias,
      arguments: args,
      directives,
      selectionSet,
      loc: this.loc(start),
    };
  }

  private parseArguments(): ArgumentNode[] {
    if (!this.peekPunct("(")) {
      return [];
    }

    this.consume();
    const args: ArgumentNode[] = [];
    while (!this.peekPunct(")")) {
      const start = this.expectKind("Name", "Expected an argument name");
      this.expectPunct(":");
      args.push({
        kind: "Argument",
        name: start.value,
        value: this.parseValue(),
        loc: this.loc(start),
      });
    }
    this.expectPunct(")");
    return args;
  }

  private parseDirectives(): DirectiveNode[] {
    const directives: DirectiveNode[] = [];
    while (this.peekPunct("@")) {
      const start = this.consume();
      const name = this.expectKind("Name", "Expected a directive name").value;
      directives.push({
        kind: "Directive",
        name,
        arguments: this.parseArguments(),
        loc: this.loc(start),
      });
    }
    return directives;
  }

  private parseValue(): ValueNode {
    const token = this.peek();
    switch (token.kind) {
      case "Punct":
        if (token.value === "$") {
          return this.parseVariableValue();
        }
        if (token.value === "[") {
          return this.parseListValue();
        }
        if (token.value === "{") {
          return this.parseObjectValue();
        }
        break;
      case "Int":
        this.consume();
        return {
          kind: "IntValue",
          value: Number(token.value),
          raw: token.value,
          loc: this.tokenLoc(token),
        };
      case "Float":
        this.consume();
        return {
          kind: "FloatValue",
          value: Number(token.value),
          raw: token.value,
          loc: this.tokenLoc(token),
        };
      case "String":
        this.consume();
        return {
          kind: "StringValue",
          value: token.value,
          loc: this.tokenLoc(token),
        };
      case "Name":
        this.consume();
        if (token.value === "true" || token.value === "false") {
          return {
            kind: "BooleanValue",
            value: token.value === "true",
            loc: this.tokenLoc(token),
          };
        }
        if (token.value === "null") {
          return {
            kind: "NullValue",
            loc: this.tokenLoc(token),
          };
        }
        return {
          kind: "EnumValue",
          value: token.value,
          loc: this.tokenLoc(token),
        };
      default:
        break;
    }

    throw this.error(token, "Expected a value");
  }

  private parseVariableValue(): VariableValueNode {
    const start = this.expectPunct("$");
    const name = this.expectKind("Name", "Expected a variable name").value;
    return {
      kind: "Variable",
      name,
      loc: this.loc(start),
    };
  }

  private parseListValue(): ListValueNode {
    const start = this.expectPunct("[");
    const values: ValueNode[] = [];
    while (!this.peekPunct("]")) {
      values.push(this.parseValue());
    }
    this.expectPunct("]");
    return {
      kind: "ListValue",
      values,
      loc: this.loc(start),
    };
  }

  private parseObjectValue(): ObjectValueNode {
    const start = this.expectPunct("{");
    const fields: ArgumentNode[] = [];
    while (!this.peekPunct("}")) {
      const name = this.expectKind("Name", "Expected an object field name");
      this.expectPunct(":");
      fields.push({
        kind: "Argument",
        name: name.value,
        value: this.parseValue(),
        loc: this.loc(name),
      });
    }
    this.expectPunct("}");
    return {
      kind: "ObjectValue",
      fields,
      loc: this.loc(start),
    };
  }

  private peek(): Token {
    return this.tokens[this.index] as Token;
  }

  private previous(): Token {
    return this.tokens[Math.max(0, this.index - 1)] as Token;
  }

  private consume(): Token {
    const token = this.peek();
    this.index += 1;
    return token;
  }

  private peekPunct(value: string): boolean {
    const token = this.peek();
    return token.kind === "Punct" && token.value === value;
  }

  private peekName(value: string): boolean {
    const token = this.peek();
    return token.kind === "Name" && token.value === value;
  }

  private expectPunct(value: string): Token {
    const token = this.peek();
    if (token.kind !== "Punct" || token.value !== value) {
      throw this.error(token, `Expected '${value}'`);
    }
    return this.consume();
  }

  private expectKind(kind: Token["kind"], message: string): Token {
    const token = this.peek();
    if (token.kind !== kind) {
      throw this.error(token, message);
    }
    return this.consume();
  }

  private loc(start: Token): SourceLocation {
    const end = this.previous();
    return {
      start: start.index,
      end: end.end,
      line: start.line,
      column: start.column,
    };
  }

  private tokenLoc(token: Token): SourceLocation {
    return {
      start: token.index,
      end: token.end,
      line: token.line,
      column: token.column,
    };
  }

  private error(token: Token, message: string): GraphQLSyntaxError {
    return new GraphQLSyntaxError(message, {
      line: token.line,
      column: token.column,
      index: token.index,
    });
  }
}

function isDigit(char: string | undefined): boolean {
  return char !== undefined && char >= "0" && char <= "9";
}

function isNameStart(char: string | undefined): boolean {
  return (
    char !== undefined &&
    ((char >= "A" && char <= "Z") ||
      (char >= "a" && char <= "z") ||
      char === "_")
  );
}

function isNameContinue(char: string | undefined): boolean {
  return isNameStart(char) || isDigit(char);
}

function blockStringValue(raw: string): string {
  const lines = raw.replace(/\r\n?/g, "\n").split("\n");
  let commonIndent: number | null = null;

  for (let index = 1; index < lines.length; index += 1) {
    const line = lines[index] as string;
    if (line.trim().length === 0) {
      continue;
    }
    const indent = line.length - line.trimStart().length;
    if (commonIndent === null || indent < commonIndent) {
      commonIndent = indent;
    }
  }

  if (commonIndent !== null && commonIndent > 0) {
    for (let index = 1; index < lines.length; index += 1) {
      lines[index] = (lines[index] as string).slice(commonIndent);
    }
  }

  while (lines.length > 0 && (lines[0] as string).trim().length === 0) {
    lines.shift();
  }
  while (
    lines.length > 0 &&
    (lines[lines.length - 1] as string).trim().length === 0
  ) {
    lines.pop();
  }

  return lines.join("\n");
}

export function parseGraphQL(source: string): DocumentNode {
  return new Parser(source).parseDocument();
}
