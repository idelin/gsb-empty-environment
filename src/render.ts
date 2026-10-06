/**
 * Renders a query AST to SQL text plus a positional binding list.
 *
 * Binding rules:
 *  - Values are bound by Param node identity (object reference), never by
 *    value equality: two nodes holding equal values get distinct
 *    placeholders; the same node referenced twice gets one placeholder.
 *  - Placeholders are numbered $1..$n in a deterministic traversal order:
 *    pre-order depth-first over columns, FROM sources, WHERE, ORDER BY,
 *    LIMIT, OFFSET; nested subqueries are numbered at the position where
 *    they appear.
 *  - Raw fragments are emitted verbatim but can never carry values: a
 *    fragment containing placeholder syntax ($n or ?) is rejected.
 *  - Identifiers are always double-quoted with embedded quotes doubled.
 *  - Empty lists: `x IN ()` renders as `(1 = 0)`, `x NOT IN ()` as
 *    `(1 = 1)`; `AND ()` as `(1 = 1)`, `OR ()` as `(1 = 0)`.
 */

import type { Expr, FromSource, Param, Select } from './ast.ts';

export class RenderError extends Error {}

const FORBIDDEN_IN_RAW = /\$\d|\?/;

export function quoteIdent(name: string): string {
  return '"' + name.replace(/"/g, '""') + '"';
}

export interface Rendered {
  readonly sql: string;
  /** bindings[i] is the value of placeholder $(i+1). */
  readonly bindings: readonly unknown[];
  /** Placeholder index (1-based) assigned to a Param node, if rendered. */
  placeholderIndex(p: Param): number | undefined;
  /** Placeholder numbers in the order they were emitted into the SQL. */
  readonly emissionOrder: readonly number[];
}

export class Renderer {
  private readonly placeholderByParam = new Map<Param, number>();
  private readonly paramByPlaceholder: Param[] = [];
  private readonly emitted: number[] = [];

  /** Assign (or reuse) the placeholder number for a Param node. */
  placeholderIndex(p: Param): number {
    const existing = this.placeholderByParam.get(p);
    if (existing !== undefined) return existing;
    const index = this.paramByPlaceholder.length + 1;
    this.paramByPlaceholder.push(p);
    this.placeholderByParam.set(p, index);
    return index;
  }

  /** Look up the placeholder number already assigned to a Param node. */
  lookup(p: Param): number | undefined {
    return this.placeholderByParam.get(p);
  }

  paramAt(index: number): Param | undefined {
    return this.paramByPlaceholder[index - 1];
  }

  get emissionOrder(): readonly number[] {
    return this.emitted;
  }

  get bindings(): readonly unknown[] {
    return this.paramByPlaceholder.map((p) => p.value);
  }

  renderSelect(q: Select): string {
    const parts: string[] = [];
    const cols =
      q.columns.length === 0
        ? '*'
        : q.columns
            .map((c) =>
              c.alias === null
                ? this.renderExpr(c.expr)
                : `${this.renderExpr(c.expr)} AS ${quoteIdent(c.alias)}`,
            )
            .join(', ');
    parts.push(`SELECT ${cols}`);
    if (q.from.length > 0) {
      parts.push(`FROM ${q.from.map((f) => this.renderFrom(f)).join(', ')}`);
    }
    if (q.where !== null) {
      parts.push(`WHERE ${this.renderExpr(q.where)}`);
    }
    if (q.orderBy.length > 0) {
      const items = q.orderBy
        .map((o) => `${this.renderExpr(o.expr)} ${o.dir}`)
        .join(', ');
      parts.push(`ORDER BY ${items}`);
    }
    if (q.limit !== null) {
      parts.push(`LIMIT ${this.renderPlaceholder(q.limit)}`);
    }
    if (q.offset !== null) {
      parts.push(`OFFSET ${this.renderPlaceholder(q.offset)}`);
    }
    return parts.join(' ');
  }

  private renderFrom(f: FromSource): string {
    if (f.kind === 'table') {
      const table = f.table.path.map(quoteIdent).join('.');
      return f.alias === null ? table : `${table} AS ${quoteIdent(f.alias)}`;
    }
    return `(${this.renderSelect(f.query)}) AS ${quoteIdent(f.alias)}`;
  }

  private renderPlaceholder(p: Param): string {
    const index = this.placeholderIndex(p);
    this.emitted.push(index);
    return `$${index}`;
  }

  renderExpr(e: Expr): string {
    switch (e.kind) {
      case 'param':
        return this.renderPlaceholder(e);
      case 'ident':
        return e.path.map(quoteIdent).join('.');
      case 'raw':
        if (FORBIDDEN_IN_RAW.test(e.text)) {
          throw new RenderError(
            `raw fragment must not contain placeholder syntax: ${JSON.stringify(e.text)}`,
          );
        }
        return e.text;
      case 'cmp':
        return `(${this.renderExpr(e.left)} ${e.op} ${this.renderExpr(e.right)})`;
      case 'in': {
        if (e.list.length === 0) {
          return e.negated ? '(1 = 1)' : '(1 = 0)';
        }
        const items = e.list.map((x) => this.renderExpr(x)).join(', ');
        return `(${this.renderExpr(e.item)} ${e.negated ? 'NOT ' : ''}IN (${items}))`;
      }
      case 'inSub':
        return `(${this.renderExpr(e.item)} ${e.negated ? 'NOT ' : ''}IN (${this.renderSelect(e.query)}))`;
      case 'and':
        if (e.items.length === 0) return '(1 = 1)';
        return `(${e.items.map((x) => this.renderExpr(x)).join(' AND ')})`;
      case 'or':
        if (e.items.length === 0) return '(1 = 0)';
        return `(${e.items.map((x) => this.renderExpr(x)).join(' OR ')})`;
      case 'not':
        return `(NOT ${this.renderExpr(e.item)})`;
      case 'scalarSub':
        return `(${this.renderSelect(e.query)})`;
      case 'exists':
        return e.negated
          ? `(NOT EXISTS (${this.renderSelect(e.query)}))`
          : `(EXISTS (${this.renderSelect(e.query)}))`;
      default: {
        const never: never = e;
        throw new RenderError(`unknown expression node: ${JSON.stringify(never)}`);
      }
    }
  }
}

export function render(query: Select): Rendered {
  const renderer = new Renderer();
  const sql = renderer.renderSelect(query);
  return {
    sql,
    bindings: renderer.bindings,
    emissionOrder: renderer.emissionOrder,
    placeholderIndex: (p: Param) => renderer.lookup(p),
  };
}
