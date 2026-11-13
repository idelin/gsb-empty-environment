export type OperationType = "query" | "mutation" | "subscription";

export interface SourceLocation {
  readonly offset: number;
}

export interface VariableNode {
  readonly kind: "variable";
  readonly name: string;
  readonly location: SourceLocation;
}

export interface StringValueNode {
  readonly kind: "string";
  readonly value: string;
  readonly location: SourceLocation;
}

export interface NumberValueNode {
  readonly kind: "number";
  readonly value: number;
  readonly location: SourceLocation;
}

export interface BooleanValueNode {
  readonly kind: "boolean";
  readonly value: boolean;
  readonly location: SourceLocation;
}

export interface NullValueNode {
  readonly kind: "null";
  readonly value: null;
  readonly location: SourceLocation;
}

export interface EnumValueNode {
  readonly kind: "enum";
  readonly value: string;
  readonly location: SourceLocation;
}

export interface ListValueNode {
  readonly kind: "list";
  readonly values: readonly ValueNode[];
  readonly location: SourceLocation;
}

export interface ObjectValueNode {
  readonly kind: "object";
  readonly fields: Readonly<Record<string, ValueNode>>;
  readonly location: SourceLocation;
}

export type ValueNode =
  | VariableNode
  | StringValueNode
  | NumberValueNode
  | BooleanValueNode
  | NullValueNode
  | EnumValueNode
  | ListValueNode
  | ObjectValueNode;

export interface FieldNode {
  readonly kind: "field";
  readonly name: string;
  readonly alias: string | null;
  readonly arguments: Readonly<Record<string, ValueNode>>;
  readonly directives: readonly DirectiveNode[];
  readonly selections: readonly SelectionNode[];
  readonly location: SourceLocation;
}

export interface FragmentSpreadNode {
  readonly kind: "fragment-spread";
  readonly name: string;
  readonly directives: readonly DirectiveNode[];
  readonly location: SourceLocation;
}

export interface InlineFragmentNode {
  readonly kind: "inline-fragment";
  readonly typeCondition: string | null;
  readonly directives: readonly DirectiveNode[];
  readonly selections: readonly SelectionNode[];
  readonly location: SourceLocation;
}

export type SelectionNode = FieldNode | FragmentSpreadNode | InlineFragmentNode;

export interface DirectiveNode {
  readonly name: string;
  readonly arguments: Readonly<Record<string, ValueNode>>;
  readonly location: SourceLocation;
}

export interface OperationNode {
  readonly kind: "operation";
  readonly operation: OperationType;
  readonly name: string | null;
  readonly selections: readonly SelectionNode[];
  readonly location: SourceLocation;
}

export interface FragmentNode {
  readonly kind: "fragment";
  readonly name: string;
  readonly typeCondition: string;
  readonly selections: readonly SelectionNode[];
  readonly location: SourceLocation;
}

export type DefinitionNode = OperationNode | FragmentNode;

export interface GraphQLDocument {
  readonly definitions: readonly DefinitionNode[];
}

export interface FieldOccurrence {
  readonly field: FieldNode;
  readonly path: readonly FieldNode[];
}

const TOKEN_NAME = 1;
const TOKEN_STRING = 2;
const TOKEN_NUMBER = 3;
const TOKEN_PUNCTUATOR = 4;
const TOKEN_EOF = 5;

interface Token {
  readonly kind: number;
  readonly value: string;
  readonly offset: number;
}

const PUNCTUATORS = new Set(["!", "$", "&", "(", ")", ":", "=", "@", "[", "]", "{", "|", "}"]);

export class GraphQLDocumentError extends Error {
  readonly offset: number;

  constructor(message: string, offset: number) {
    super(`${message} at offset ${offset}`);
    this.name = "GraphQLDocumentError";
    this.offset = offset;
  }
}

function tokenize(source: string): Token[] {
  const tokens: Token[] = [];
  let index = 0;

  while (index < source.length) {
    const char = source[index];

    if (
      char === "\uFEFF" ||
      char === "\u0009" ||
      char === "\u000A" ||
      char === "\u000D" ||
      char === "\u0020" ||
      char === "," ||
      char === "\u000B" ||
      char === "\u000C"
    ) {
      index += 1;
      continue;
    }

    if (char === "#") {
      index += 1;
      while (index < source.length) {
        const next = source[index];
        if (next === "\n" || next === "\r") {
          break;
        }
        index += 1;
      }
      continue;
    }

    if (char === ".") {
      if (source.slice(index, index + 3) !== "...") {
        throw new GraphQLDocumentError("Expected a spread operator", index);
      }
      tokens.push({ kind: TOKEN_PUNCTUATOR, value: "...", offset: index });
      index += 3;
      continue;
    }

    if (PUNCTUATORS.has(char)) {
      tokens.push({ kind: TOKEN_PUNCTUATOR, value: char, offset: index });
      index += 1;
      continue;
    }

    if (char === '"') {
      const start = index;
      if (source.slice(index, index + 3) === '"""') {
        index += 3;
        let value = "";
        while (index < source.length) {
          if (source.slice(index, index + 3) === '"""') {
            index += 3;
            tokens.push({ kind: TOKEN_STRING, value, offset: start });
            break;
          }
          if (source[index] === "\\" && source.slice(index, index + 4) === '\\"""') {
            value += '"""';
            index += 4;
            continue;
          }
          value += source[index];
          index += 1;
        }
        if (index > source.length || tokens[tokens.length - 1]?.offset !== start) {
          throw new GraphQLDocumentError("Unterminated block string", start);
        }
        continue;
      }

      index += 1;
      let value = "";
      let closed = false;
      while (index < source.length) {
        const next = source[index];
        if (next === '"') {
          index += 1;
          closed = true;
          break;
        }
        if (next === "\n" || next === "\r") {
          throw new GraphQLDocumentError("Unterminated string", start);
        }
        if (next === "\\") {
          const escape = readEscape(source, index);
          value += escape.value;
          index = escape.nextIndex;
          continue;
        }
        value += next;
        index += 1;
      }
      if (!closed) {
        throw new GraphQLDocumentError("Unterminated string", start);
      }
      tokens.push({ kind: TOKEN_STRING, value, offset: start });
      continue;
    }

    if (/[0-9]/.test(char) || (char === "-" && /[0-9]/.test(source[index + 1] ?? ""))) {
      const start = index;
      if (source[index] === "-") {
        index += 1;
      }
      while (index < source.length && /[0-9]/.test(source[index])) {
        index += 1;
      }
      if (source[index] === ".") {
        index += 1;
        while (index < source.length && /[0-9]/.test(source[index])) {
          index += 1;
        }
      }
      if (source[index] === "e" || source[index] === "E") {
        index += 1;
        if (source[index] === "+" || source[index] === "-") {
          index += 1;
        }
        while (index < source.length && /[0-9]/.test(source[index])) {
          index += 1;
        }
      }
      tokens.push({ kind: TOKEN_NUMBER, value: source.slice(start, index), offset: start });
      continue;
    }

    if (/[A-Za-z_]/.test(char)) {
      const start = index;
      index += 1;
      while (index < source.length && /[A-Za-z0-9_]/.test(source[index])) {
        index += 1;
      }
      tokens.push({ kind: TOKEN_NAME, value: source.slice(start, index), offset: start });
      continue;
    }

    throw new GraphQLDocumentError(`Unexpected character ${JSON.stringify(char)}`, index);
  }

  tokens.push({ kind: TOKEN_EOF, value: "", offset: source.length });
  return tokens;
}

function readEscape(source: string, slashIndex: number): { value: string; nextIndex: number } {
  const escaped = source[slashIndex + 1];
  if (escaped === undefined) {
    throw new GraphQLDocumentError("Unterminated escape sequence", slashIndex);
  }

  const simpleEscapes: Record<string, string> = {
    '"': '"',
    "\\": "\\",
    "/": "/",
    b: "\b",
    f: "\f",
    n: "\n",
    r: "\r",
    t: "\t",
  };
  if (simpleEscapes[escaped] !== undefined) {
    return { value: simpleEscapes[escaped], nextIndex: slashIndex + 2 };
  }

  if (escaped === "u") {
    const hex = source.slice(slashIndex + 2, slashIndex + 6);
    if (!/^[0-9A-Fa-f]{4}$/.test(hex)) {
      throw new GraphQLDocumentError("Invalid unicode escape sequence", slashIndex);
    }
    return {
      value: String.fromCodePoint(Number.parseInt(hex, 16)),
      nextIndex: slashIndex + 6,
    };
  }

  throw new GraphQLDocumentError(`Invalid escape sequence \\${escaped}`, slashIndex);
}

class Parser {
  private readonly tokens: readonly Token[];
  private index = 0;

  constructor(source: string) {
    this.tokens = tokenize(source);
  }

  parseDocument(): GraphQLDocument {
    const definitions: DefinitionNode[] = [];

    while (!this.peekIs(TOKEN_EOF)) {
      if (this.peekPunctuator("{")) {
        definitions.push(this.parseOperation(null));
        continue;
      }

      const keyword = this.peekName();
      if (keyword === "query" || keyword === "mutation" || keyword === "subscription") {
        definitions.push(this.parseOperation(keyword));
        continue;
      }
      if (keyword === "fragment") {
        definitions.push(this.parseFragment());
        continue;
      }

      throw this.error("Expected an operation or fragment definition");
    }

    if (definitions.length === 0) {
      throw this.error("The GraphQL document is empty");
    }

    return { definitions };
  }

  private parseOperation(operation: OperationType | null): OperationNode {
    const location = this.peek();
    let actualOperation: OperationType = "query";
    let name: string | null = null;

    if (operation !== null) {
      this.advance();
      actualOperation = operation;
      if (this.peekIs(TOKEN_NAME)) {
        name = this.advance().value;
      }
      if (this.peekPunctuator("(")) {
        this.skipBalancedParentheses();
      }
      this.parseDirectives();
    }

    if (!this.peekPunctuator("{")) {
      throw this.error("Expected a selection set");
    }
    const selections = this.parseSelectionSet();

    return {
      kind: "operation",
      operation: actualOperation,
      name,
      selections,
      location: { offset: location.offset },
    };
  }

  private parseFragment(): FragmentNode {
    const location = this.advance();
    const name = this.expectName("Expected a fragment name");

    if (this.peekName() !== "on") {
      throw this.error('Expected "on" after the fragment name');
    }
    this.advance();
    const typeCondition = this.expectName("Expected a fragment type condition");
    this.parseDirectives();

    if (!this.peekPunctuator("{")) {
      throw this.error("Expected a fragment selection set");
    }

    return {
      kind: "fragment",
      name,
      typeCondition,
      selections: this.parseSelectionSet(),
      location: { offset: location.offset },
    };
  }

  private parseSelectionSet(): SelectionNode[] {
    this.expectPunctuator("{");
    const selections: SelectionNode[] = [];
    while (!this.peekPunctuator("}")) {
      if (this.peekIs(TOKEN_EOF)) {
        throw this.error("Unterminated selection set");
      }
      selections.push(this.parseSelection());
    }
    this.expectPunctuator("}");
    return selections;
  }

  private parseSelection(): SelectionNode {
    if (this.peekPunctuator("...")) {
      return this.parseFragmentSelection();
    }
    return this.parseField();
  }

  private parseFragmentSelection(): FragmentSpreadNode | InlineFragmentNode {
    const location = this.advance();

    if (this.peekName() === "on") {
      this.advance();
      const typeCondition = this.expectName("Expected an inline fragment type condition");
      const directives = this.parseDirectives();
      if (!this.peekPunctuator("{")) {
        throw this.error("Expected an inline fragment selection set");
      }
      return {
        kind: "inline-fragment",
        typeCondition,
        directives,
        selections: this.parseSelectionSet(),
        location: { offset: location.offset },
      };
    }

    if (this.peekIs(TOKEN_NAME)) {
      const name = this.advance().value;
      return {
        kind: "fragment-spread",
        name,
        directives: this.parseDirectives(),
        location: { offset: location.offset },
      };
    }

    const directives = this.parseDirectives();
    if (!this.peekPunctuator("{")) {
      throw this.error("Expected an inline fragment selection set");
    }
    return {
      kind: "inline-fragment",
      typeCondition: null,
      directives,
      selections: this.parseSelectionSet(),
      location: { offset: location.offset },
    };
  }

  private parseField(): FieldNode {
    const location = this.peek();
    let name = this.expectName("Expected a field name");
    let alias: string | null = null;

    if (this.peekPunctuator(":")) {
      this.advance();
      alias = name;
      name = this.expectName("Expected a field name after the alias");
    }

    const args = this.parseArguments();
    const directives = this.parseDirectives();
    const selections = this.peekPunctuator("{") ? this.parseSelectionSet() : [];

    return {
      kind: "field",
      name,
      alias,
      arguments: args,
      directives,
      selections,
      location: { offset: location.offset },
    };
  }

  private parseArguments(): Record<string, ValueNode> {
    const args: Record<string, ValueNode> = {};
    if (!this.peekPunctuator("(")) {
      return args;
    }

    this.advance();
    while (!this.peekPunctuator(")")) {
      const name = this.expectName("Expected an argument name");
      this.expectPunctuator(":");
      args[name] = this.parseValue();
    }
    this.expectPunctuator(")");
    return args;
  }

  private parseDirectives(): DirectiveNode[] {
    const directives: DirectiveNode[] = [];
    while (this.peekPunctuator("@")) {
      const location = this.advance();
      const name = this.expectName("Expected a directive name");
      directives.push({
        name,
        arguments: this.parseArguments(),
        location: { offset: location.offset },
      });
    }
    return directives;
  }

  private parseValue(): ValueNode {
    const token = this.peek();
    const location = { offset: token.offset };

    if (this.peekPunctuator("$")) {
      this.advance();
      return {
        kind: "variable",
        name: this.expectName("Expected a variable name"),
        location,
      };
    }

    if (token.kind === TOKEN_STRING) {
      this.advance();
      return { kind: "string", value: token.value, location };
    }

    if (token.kind === TOKEN_NUMBER) {
      this.advance();
      return { kind: "number", value: Number(token.value), location };
    }

    if (this.peekPunctuator("[")) {
      this.advance();
      const values: ValueNode[] = [];
      while (!this.peekPunctuator("]")) {
        values.push(this.parseValue());
      }
      this.expectPunctuator("]");
      return { kind: "list", values, location };
    }

    if (this.peekPunctuator("{")) {
      this.advance();
      const fields: Record<string, ValueNode> = {};
      while (!this.peekPunctuator("}")) {
        const name = this.expectName("Expected an object field name");
        this.expectPunctuator(":");
        fields[name] = this.parseValue();
      }
      this.expectPunctuator("}");
      return { kind: "object", fields, location };
    }

    if (token.kind === TOKEN_NAME) {
      this.advance();
      if (token.value === "true" || token.value === "false") {
        return { kind: "boolean", value: token.value === "true", location };
      }
      if (token.value === "null") {
        return { kind: "null", value: null, location };
      }
      return { kind: "enum", value: token.value, location };
    }

    throw this.error("Expected a value");
  }

  private skipBalancedParentheses(): void {
    this.expectPunctuator("(");
    let depth = 1;
    while (depth > 0) {
      const token = this.advance();
      if (token.kind === TOKEN_EOF) {
        throw this.error("Unterminated variable definitions", token.offset);
      }
      if (token.kind === TOKEN_PUNCTUATOR && token.value === "(") {
        depth += 1;
      } else if (token.kind === TOKEN_PUNCTUATOR && token.value === ")") {
        depth -= 1;
      }
    }
  }

  private peek(): Token {
    return this.tokens[this.index];
  }

  private advance(): Token {
    const token = this.peek();
    this.index += 1;
    return token;
  }

  private peekIs(kind: number): boolean {
    return this.peek().kind === kind;
  }

  private peekPunctuator(value: string): boolean {
    const token = this.peek();
    return token.kind === TOKEN_PUNCTUATOR && token.value === value;
  }

  private peekName(): string | null {
    const token = this.peek();
    return token.kind === TOKEN_NAME ? token.value : null;
  }

  private expectName(message: string): string {
    if (!this.peekIs(TOKEN_NAME)) {
      throw this.error(message);
    }
    return this.advance().value;
  }

  private expectPunctuator(value: string): void {
    if (!this.peekPunctuator(value)) {
      throw this.error(`Expected ${JSON.stringify(value)}`);
    }
    this.advance();
  }

  private error(message: string, offset = this.peek().offset): GraphQLDocumentError {
    return new GraphQLDocumentError(message, offset);
  }
}

export function parseGraphQLDocument(source: string): GraphQLDocument {
  return new Parser(source).parseDocument();
}

export function selectOperation(
  document: GraphQLDocument,
  operationName?: string | null,
): OperationNode {
  const operations = document.definitions.filter(
    (definition): definition is OperationNode => definition.kind === "operation",
  );

  if (operations.length === 0) {
    throw new GraphQLDocumentError("No operation was provided", 0);
  }

  if (operationName !== undefined && operationName !== null) {
    const selected = operations.find((operation) => operation.name === operationName);
    if (selected === undefined) {
      throw new GraphQLDocumentError(`Unknown operation named ${JSON.stringify(operationName)}`, 0);
    }
    return selected;
  }

  if (operations.length > 1) {
    throw new GraphQLDocumentError("operationName is required when a document has multiple operations", 0);
  }

  return operations[0];
}

export function fragmentMap(document: GraphQLDocument): ReadonlyMap<string, FragmentNode> {
  const fragments = new Map<string, FragmentNode>();
  for (const definition of document.definitions) {
    if (definition.kind === "fragment") {
      if (fragments.has(definition.name)) {
        throw new GraphQLDocumentError(
          `Duplicate fragment ${JSON.stringify(definition.name)}`,
          definition.location.offset,
        );
      }
      fragments.set(definition.name, definition);
    }
  }
  return fragments;
}

function resolveFragment(
  fragments: ReadonlyMap<string, FragmentNode>,
  name: string,
  location: SourceLocation,
): FragmentNode {
  const fragment = fragments.get(name);
  if (fragment === undefined) {
    throw new GraphQLDocumentError(`Unknown fragment ${JSON.stringify(name)}`, location.offset);
  }
  return fragment;
}

export function queryDepth(
  document: GraphQLDocument,
  operation: OperationNode,
): number {
  const fragments = fragmentMap(document);
  const activeFragments = new Set<string>();

  const visit = (selections: readonly SelectionNode[]): number => {
    let maximum = 0;

    for (const selection of selections) {
      if (selection.kind === "field") {
        const nested = visit(selection.selections);
        maximum = Math.max(maximum, 1 + nested);
        continue;
      }

      if (selection.kind === "inline-fragment") {
        maximum = Math.max(maximum, visit(selection.selections));
        continue;
      }

      if (activeFragments.has(selection.name)) {
        throw new GraphQLDocumentError(
          `Fragment cycle detected at ${JSON.stringify(selection.name)}`,
          selection.location.offset,
        );
      }
      const fragment = resolveFragment(fragments, selection.name, selection.location);
      activeFragments.add(selection.name);
      maximum = Math.max(maximum, visit(fragment.selections));
      activeFragments.delete(selection.name);
    }

    return maximum;
  };

  return visit(operation.selections);
}

export function findFieldOccurrences(
  document: GraphQLDocument,
  operation: OperationNode,
): FieldOccurrence[] {
  const fragments = fragmentMap(document);
  const activeFragments = new Set<string>();
  const occurrences: FieldOccurrence[] = [];

  const visit = (
    selections: readonly SelectionNode[],
    path: readonly FieldNode[],
  ): void => {
    for (const selection of selections) {
      if (selection.kind === "field") {
        const nextPath = [...path, selection];
        occurrences.push({ field: selection, path: nextPath });
        visit(selection.selections, nextPath);
        continue;
      }

      if (selection.kind === "inline-fragment") {
        visit(selection.selections, path);
        continue;
      }

      if (activeFragments.has(selection.name)) {
        throw new GraphQLDocumentError(
          `Fragment cycle detected at ${JSON.stringify(selection.name)}`,
          selection.location.offset,
        );
      }
      const fragment = resolveFragment(fragments, selection.name, selection.location);
      activeFragments.add(selection.name);
      visit(fragment.selections, path);
      activeFragments.delete(selection.name);
    }
  };

  visit(operation.selections, []);
  return occurrences;
}

export function containsIntrospection(
  document: GraphQLDocument,
  operation: OperationNode,
): boolean {
  return findFieldOccurrences(document, operation).some(
    ({ field }) => field.name === "__schema" || field.name === "__type",
  );
}

export function collectSelectionsForType(
  document: GraphQLDocument,
  selections: readonly SelectionNode[],
  typename: string,
): FieldNode[] {
  const fragments = fragmentMap(document);
  const activeFragments = new Set<string>();
  const fields: FieldNode[] = [];

  const visit = (nodes: readonly SelectionNode[]): void => {
    for (const selection of nodes) {
      if (selection.kind === "field") {
        fields.push(selection);
        continue;
      }

      if (selection.kind === "inline-fragment") {
        if (
          selection.typeCondition === null ||
          selection.typeCondition === typename ||
          selection.typeCondition === "_Entity"
        ) {
          visit(selection.selections);
        }
        continue;
      }

      if (activeFragments.has(selection.name)) {
        throw new GraphQLDocumentError(
          `Fragment cycle detected at ${JSON.stringify(selection.name)}`,
          selection.location.offset,
        );
      }
      const fragment = resolveFragment(fragments, selection.name, selection.location);
      if (
        fragment.typeCondition === typename ||
        fragment.typeCondition === "_Entity" ||
        fragment.typeCondition === "_Any"
      ) {
        activeFragments.add(selection.name);
        visit(fragment.selections);
        activeFragments.delete(selection.name);
      }
    }
  };

  visit(selections);
  return fields;
}

export function responseKey(field: FieldNode): string {
  return field.alias ?? field.name;
}

export function resolveValue(
  value: ValueNode,
  variables: Readonly<Record<string, unknown>>,
): unknown {
  switch (value.kind) {
    case "variable":
      return variables[value.name];
    case "string":
    case "enum":
      return value.value;
    case "number":
    case "boolean":
      return value.value;
    case "null":
      return null;
    case "list":
      return value.values.map((item) => resolveValue(item, variables));
    case "object": {
      const resolved: Record<string, unknown> = {};
      for (const [key, item] of Object.entries(value.fields)) {
        resolved[key] = resolveValue(item, variables);
      }
      return resolved;
    }
    default:
      return undefined;
  }
}
