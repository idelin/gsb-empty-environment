export interface AssemblyRef {
  name: string;
  version: string | null;
  publicKeyToken: string | null;
}

export interface TypeNode {
  typeName: string;
  assembly: AssemblyRef | null;
  genericArgs: TypeNode[];
  arrayRanks: number[];
}

export interface WhitelistEntry {
  typeName: string;
  assemblyName: string;
  version: string;
  publicKeyToken: string;
}

export interface BinderOptions {
  maxDepth?: number;
}

export class TypeNameParseError extends Error {}

const DEFAULT_MAX_DEPTH = 32;

export function parseTypeName(input: string, options: BinderOptions = {}): TypeNode {
  if (typeof input !== 'string' || input.trim().length === 0) {
    throw new TypeNameParseError('type name must be a non-empty string');
  }
  const parser = new Parser(input, options.maxDepth ?? DEFAULT_MAX_DEPTH);
  return parser.parse();
}

export function isTypeAllowed(
  input: string,
  whitelist: readonly WhitelistEntry[],
  options: BinderOptions = {},
): boolean {
  let node: TypeNode;
  try {
    node = parseTypeName(input, options);
  } catch (err) {
    if (err instanceof TypeNameParseError) return false;
    throw err;
  }
  return matchesWhitelist(node, whitelist);
}

function matchesWhitelist(node: TypeNode, whitelist: readonly WhitelistEntry[]): boolean {
  const assembly = node.assembly;
  if (assembly === null || assembly.version === null || assembly.publicKeyToken === null) {
    return false;
  }
  const allowed = whitelist.some(
    (entry) =>
      entry.typeName === node.typeName &&
      entry.assemblyName === assembly.name &&
      entry.version === assembly.version &&
      entry.publicKeyToken === assembly.publicKeyToken,
  );
  if (!allowed) return false;
  return node.genericArgs.every((arg) => matchesWhitelist(arg, whitelist));
}

class Parser {
  private pos = 0;
  private input: string;
  private maxDepth: number;

  constructor(input: string, maxDepth: number) {
    this.input = input;
    this.maxDepth = maxDepth;
  }

  parse(): TypeNode {
    const node = this.parseType(0);
    this.skipWhitespace();
    if (this.pos < this.input.length) {
      throw new TypeNameParseError(`unexpected trailing characters at offset ${this.pos}`);
    }
    return node;
  }

  private parseType(depth: number): TypeNode {
    if (depth > this.maxDepth) {
      throw new TypeNameParseError(`generic nesting exceeds max depth ${this.maxDepth}`);
    }
    this.skipWhitespace();
    const typeName = this.readTypeName();
    const node: TypeNode = { typeName, assembly: null, genericArgs: [], arrayRanks: [] };

    while (this.peek() === '[') {
      if (this.peek(1) === '[') {
        this.pos++;
        node.genericArgs.push(this.parseGenericArgument(depth));
        while (this.peek() === ',') {
          this.pos++;
          node.genericArgs.push(this.parseGenericArgument(depth));
        }
        this.expect(']');
      } else {
        this.pos++;
        let rank = 1;
        while (this.peek() === ',') {
          rank++;
          this.pos++;
        }
        this.expect(']');
        node.arrayRanks.push(rank);
      }
    }

    if (this.peek() === ',') {
      node.assembly = this.parseAssembly();
    }
    return node;
  }

  private parseGenericArgument(depth: number): TypeNode {
    this.expect('[');
    const node = this.parseType(depth + 1);
    this.expect(']');
    return node;
  }

  private parseAssembly(): AssemblyRef {
    this.expect(',');
    const name = this.readUntilDelimiter().trim();
    if (name.length === 0) {
      throw new TypeNameParseError(`missing assembly name at offset ${this.pos}`);
    }
    let version: string | null = null;
    let publicKeyToken: string | null = null;
    while (this.peek() === ',') {
      this.pos++;
      const key = this.readUntil('=').trim().toLowerCase();
      this.expect('=');
      const value = this.readUntilDelimiter().trim();
      if (key === 'version') version = value;
      else if (key === 'publickeytoken') publicKeyToken = value;
    }
    return { name, version, publicKeyToken };
  }

  private readTypeName(): string {
    const start = this.pos;
    while (this.pos < this.input.length) {
      const ch = this.input[this.pos];
      if (ch === '[' || ch === ']' || ch === ',') break;
      this.pos++;
    }
    const name = this.input.slice(start, this.pos).trim();
    if (name.length === 0) {
      throw new TypeNameParseError(`missing type name at offset ${start}`);
    }
    return name;
  }

  private readUntilDelimiter(): string {
    const start = this.pos;
    while (this.pos < this.input.length) {
      const ch = this.input[this.pos];
      if (ch === ',' || ch === ']') break;
      this.pos++;
    }
    return this.input.slice(start, this.pos);
  }

  private readUntil(target: string): string {
    const start = this.pos;
    while (this.pos < this.input.length && this.input[this.pos] !== target) {
      this.pos++;
    }
    return this.input.slice(start, this.pos);
  }

  private peek(offset = 0): string {
    return this.input[this.pos + offset] ?? '';
  }

  private skipWhitespace(): void {
    while (this.input[this.pos] === ' ' || this.input[this.pos] === '\t') {
      this.pos++;
    }
  }

  private expect(ch: string): void {
    if (this.input[this.pos] !== ch) {
      throw new TypeNameParseError(`expected '${ch}' at offset ${this.pos}`);
    }
    this.pos++;
  }
}
