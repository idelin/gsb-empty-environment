/**
 * 类型引用字符串（如 "[User!]!"）与 AST TypeNode 的解析、比较工具。
 */

import type { TypeNode } from './types.ts';

export type ParsedTypeRef =
  | { kind: 'Named'; name: string }
  | { kind: 'List'; inner: ParsedTypeRef }
  | { kind: 'NonNull'; inner: ParsedTypeRef };

class TypeRefScanner {
  private pos = 0;
  private readonly ref: string;

  constructor(ref: string) {
    this.ref = ref;
  }

  parse(): ParsedTypeRef {
    const type = this.parseType();
    this.skipSpaces();
    if (this.pos !== this.ref.length) {
      throw new Error(`Malformed type reference "${this.ref}"`);
    }
    return type;
  }

  private skipSpaces(): void {
    while (this.pos < this.ref.length && this.ref[this.pos] === ' ') {
      this.pos += 1;
    }
  }

  private parseType(): ParsedTypeRef {
    this.skipSpaces();
    let type: ParsedTypeRef;
    if (this.ref[this.pos] === '[') {
      this.pos += 1;
      const inner = this.parseType();
      this.skipSpaces();
      if (this.ref[this.pos] !== ']') {
        throw new Error(`Malformed list type reference "${this.ref}"`);
      }
      this.pos += 1;
      type = { kind: 'List', inner };
    } else {
      const start = this.pos;
      while (
        this.pos < this.ref.length &&
        /[A-Za-z0-9_]/.test(this.ref[this.pos])
      ) {
        this.pos += 1;
      }
      if (start === this.pos) {
        throw new Error(`Malformed named type reference "${this.ref}"`);
      }
      type = { kind: 'Named', name: this.ref.slice(start, this.pos) };
    }
    this.skipSpaces();
    if (this.ref[this.pos] === '!') {
      this.pos += 1;
      type = { kind: 'NonNull', inner: type };
    }
    return type;
  }
}

export function parseTypeRef(ref: string): ParsedTypeRef {
  return new TypeRefScanner(ref).parse();
}

export function refNamedName(ref: ParsedTypeRef): string {
  let current = ref;
  while (current.kind !== 'Named') {
    current = current.inner;
  }
  return current.name;
}

export function refIsList(ref: ParsedTypeRef): boolean {
  const inner = ref.kind === 'NonNull' ? ref.inner : ref;
  return inner.kind === 'List';
}

export function refIsNonNull(ref: ParsedTypeRef): boolean {
  return ref.kind === 'NonNull';
}

export function typeNodeToString(node: TypeNode): string {
  switch (node.kind) {
    case 'NamedType':
      return node.name;
    case 'ListType':
      return `[${typeNodeToString(node.type)}]`;
    case 'NonNullType':
      return `${typeNodeToString(node.type)}!`;
  }
}

export function typeNodeNamedName(node: TypeNode): string {
  let current: TypeNode = node;
  while (current.kind !== 'NamedType') {
    current = current.type;
  }
  return current.name;
}

/**
 * GraphQL 变量可赋值规则（IsTypeSubTypeOf）：
 * 变量类型必须与参数位置结构一致；非空变量可传入可空位置，反之不可；
 * 具名类型名必须一致，列表结构必须一致。
 */
export function isVariableAssignable(
  variable: TypeNode,
  expected: ParsedTypeRef,
): boolean {
  if (expected.kind === 'NonNull') {
    if (variable.kind === 'NonNullType') {
      return isVariableAssignable(variable.type, expected.inner);
    }
    return isVariableAssignable(variable, expected.inner);
  }
  if (variable.kind === 'NonNullType') {
    return isVariableAssignable(variable.type, expected);
  }
  if (expected.kind === 'List') {
    if (variable.kind !== 'ListType') {
      return false;
    }
    return isVariableAssignable(variable.type, expected.inner);
  }
  if (variable.kind !== 'NamedType') {
    return false;
  }
  return variable.name === expected.name;
}

const SCALAR_LITERAL_KINDS = new Set([
  'IntValue',
  'FloatValue',
  'StringValue',
  'BooleanValue',
  'NullValue',
  'EnumValue',
]);

export function isScalarLiteralKind(kind: string): boolean {
  return SCALAR_LITERAL_KINDS.has(kind);
}
