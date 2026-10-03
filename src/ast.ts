/** Source position of an AST node (1-based line / column). */
export interface SourcePos {
  readonly line: number;
  readonly column: number;
}

export const at = (line: number, column: number): SourcePos => ({ line, column });

export function formatPos(pos: SourcePos): string {
  return `${pos.line}:${pos.column}`;
}

let nextNodeId = 0;

/** Reset the global node id counter (mainly for deterministic tests). */
export function resetNodeIds(): void {
  nextNodeId = 0;
}

/**
 * Generic AST node. The attribute-grammar framework is language-agnostic:
 * semantics are attached to `kind`, and node-specific data lives in `fields`.
 */
export class AstNode {
  readonly id: number;
  parent: AstNode | null = null;
  /** Index of this node within `parent.children` (-1 when detached). */
  childIndex = -1;
  readonly children: AstNode[] = [];

  constructor(
    readonly kind: string,
    readonly pos: SourcePos,
    readonly fields: Record<string, unknown> = {},
  ) {
    this.id = nextNodeId++;
  }

  add(child: AstNode): this {
    child.parent = this;
    child.childIndex = this.children.length;
    this.children.push(child);
    return this;
  }

  field<T>(name: string): T {
    return this.fields[name] as T;
  }

  toString(): string {
    return `${this.kind}#${this.id}`;
  }
}
