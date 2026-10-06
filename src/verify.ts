/**
 * Render-then-reparse verification.
 *
 * Both the source AST and the re-parsed SQL are walked in the renderer's
 * deterministic traversal order (pre-order: columns, FROM, WHERE, ORDER BY,
 * LIMIT, OFFSET; subqueries at the position where they appear). The walk
 * yields, per side, the sequence of Param nodes / placeholder numbers and
 * the sequence of identifier paths. Verification checks that:
 *
 *  1. every Param node occurrence maps to the placeholder number found at
 *     the same traversal position after re-parsing (binding correspondence);
 *  2. bindings[i] is exactly the value of the Param assigned placeholder
 *     i+1 (identity, not value equality);
 *  3. identifier paths round-trip through quoting/escaping unchanged.
 */

import type { Expr, Param, Select } from './ast.ts';
import { parseSelect } from './parse.ts';
import type { PExpr, PSelect } from './parse.ts';
import { render } from './render.ts';

// -- source-side collectors ---------------------------------------------------

export function collectParams(query: Select): Param[] {
  const out: Param[] = [];
  const walkExpr = (e: Expr): void => {
    switch (e.kind) {
      case 'param':
        out.push(e);
        return;
      case 'ident':
      case 'raw':
        return;
      case 'cmp':
        walkExpr(e.left);
        walkExpr(e.right);
        return;
      case 'in':
        // The renderer drops the item expression for an empty list
        // (constant predicate), so collectors must skip it too.
        if (e.list.length === 0) return;
        walkExpr(e.item);
        e.list.forEach(walkExpr);
        return;
      case 'inSub':
        walkExpr(e.item);
        walkSelect(e.query);
        return;
      case 'and':
      case 'or':
        e.items.forEach(walkExpr);
        return;
      case 'not':
        walkExpr(e.item);
        return;
      case 'scalarSub':
        walkSelect(e.query);
        return;
      case 'exists':
        walkSelect(e.query);
        return;
    }
  };
  const walkSelect = (q: Select): void => {
    for (const c of q.columns) walkExpr(c.expr);
    for (const f of q.from) {
      if (f.kind === 'subquery') walkSelect(f.query);
    }
    if (q.where !== null) walkExpr(q.where);
    for (const o of q.orderBy) walkExpr(o.expr);
    if (q.limit !== null) out.push(q.limit);
    if (q.offset !== null) out.push(q.offset);
  };
  walkSelect(query);
  return out;
}

export function collectIdents(query: Select): string[][] {
  const out: string[][] = [];
  const walkExpr = (e: Expr): void => {
    switch (e.kind) {
      case 'ident':
        out.push([...e.path]);
        return;
      case 'param':
      case 'raw':
        return;
      case 'cmp':
        walkExpr(e.left);
        walkExpr(e.right);
        return;
      case 'in':
        if (e.list.length === 0) return;
        walkExpr(e.item);
        e.list.forEach(walkExpr);
        return;
      case 'inSub':
        walkExpr(e.item);
        walkSelect(e.query);
        return;
      case 'and':
      case 'or':
        e.items.forEach(walkExpr);
        return;
      case 'not':
        walkExpr(e.item);
        return;
      case 'scalarSub':
        walkSelect(e.query);
        return;
      case 'exists':
        walkSelect(e.query);
        return;
    }
  };
  const walkSelect = (q: Select): void => {
    for (const c of q.columns) {
      walkExpr(c.expr);
      if (c.alias !== null) out.push([c.alias]);
    }
    for (const f of q.from) {
      if (f.kind === 'table') {
        out.push([...f.table.path]);
        if (f.alias !== null) out.push([f.alias]);
      } else {
        walkSelect(f.query);
        out.push([f.alias]);
      }
    }
    if (q.where !== null) walkExpr(q.where);
    for (const o of q.orderBy) walkExpr(o.expr);
  };
  walkSelect(query);
  return out;
}

// -- parsed-side collectors -----------------------------------------------------

export function collectPlaceholders(query: PSelect): number[] {
  const out: number[] = [];
  const walkExpr = (e: PExpr): void => {
    switch (e.kind) {
      case 'placeholder':
        out.push(e.index);
        return;
      case 'ident':
      case 'number':
      case 'string':
      case 'star':
        return;
      case 'call':
        e.args.forEach(walkExpr);
        return;
      case 'cmp':
        walkExpr(e.left);
        walkExpr(e.right);
        return;
      case 'in':
        walkExpr(e.item);
        e.list.forEach(walkExpr);
        return;
      case 'inSub':
        walkExpr(e.item);
        walkSelect(e.query);
        return;
      case 'and':
      case 'or':
        e.items.forEach(walkExpr);
        return;
      case 'not':
        walkExpr(e.item);
        return;
      case 'scalarSub':
        walkSelect(e.query);
        return;
      case 'exists':
        walkSelect(e.query);
        return;
    }
  };
  const walkSelect = (q: PSelect): void => {
    for (const c of q.columns) walkExpr(c.expr);
    for (const f of q.from) {
      if (f.kind === 'subquery') walkSelect(f.query);
    }
    if (q.where !== null) walkExpr(q.where);
    for (const o of q.orderBy) walkExpr(o.expr);
    if (q.limit !== null) walkExpr(q.limit);
    if (q.offset !== null) walkExpr(q.offset);
  };
  walkSelect(query);
  return out;
}

export function collectParsedIdents(query: PSelect): string[][] {
  const out: string[][] = [];
  const walkExpr = (e: PExpr): void => {
    switch (e.kind) {
      case 'ident':
        out.push([...e.path]);
        return;
      case 'placeholder':
      case 'number':
      case 'string':
      case 'star':
        return;
      case 'call':
        e.args.forEach(walkExpr);
        return;
      case 'cmp':
        walkExpr(e.left);
        walkExpr(e.right);
        return;
      case 'in':
        walkExpr(e.item);
        e.list.forEach(walkExpr);
        return;
      case 'inSub':
        walkExpr(e.item);
        walkSelect(e.query);
        return;
      case 'and':
      case 'or':
        e.items.forEach(walkExpr);
        return;
      case 'not':
        walkExpr(e.item);
        return;
      case 'scalarSub':
        walkSelect(e.query);
        return;
      case 'exists':
        walkSelect(e.query);
        return;
    }
  };
  const walkSelect = (q: PSelect): void => {
    for (const c of q.columns) {
      walkExpr(c.expr);
      if (c.alias !== null) out.push([c.alias]);
    }
    for (const f of q.from) {
      if (f.kind === 'table') {
        out.push([...f.path]);
        if (f.alias !== null) out.push([f.alias]);
      } else {
        walkSelect(f.query);
        out.push([f.alias]);
      }
    }
    if (q.where !== null) walkExpr(q.where);
    for (const o of q.orderBy) walkExpr(o.expr);
  };
  walkSelect(query);
  return out;
}

// -- round-trip verification ----------------------------------------------------

export interface Verification {
  readonly ok: boolean;
  readonly problems: readonly string[];
  readonly sql: string;
  readonly bindings: readonly unknown[];
}

export function verifyRoundTrip(query: Select): Verification {
  const problems: string[] = [];
  const rendered = render(query);
  const parsed = parseSelect(rendered.sql);

  const params = collectParams(query);
  const holes = collectPlaceholders(parsed);

  if (params.length !== holes.length) {
    problems.push(
      `param occurrence count ${params.length} != placeholder count ${holes.length}`,
    );
  } else {
    for (let i = 0; i < params.length; i++) {
      const assigned = rendered.placeholderIndex(params[i]);
      if (assigned !== holes[i]) {
        problems.push(
          `occurrence ${i}: param assigned $${assigned} but parsed placeholder is $${holes[i]}`,
        );
      }
    }
  }

  for (let i = 0; i < rendered.bindings.length; i++) {
    const p = params.find((_, occ) => rendered.placeholderIndex(params[occ]) === i + 1);
    if (p === undefined) {
      problems.push(`placeholder $${i + 1} has no source param`);
    } else if (!Object.is(rendered.bindings[i], p.value)) {
      problems.push(`bindings[$${i + 1}] does not match its param value`);
    }
  }

  const maxHole = holes.reduce((a, b) => Math.max(a, b), 0);
  if (maxHole !== rendered.bindings.length) {
    problems.push(
      `max placeholder $${maxHole} != bindings length ${rendered.bindings.length}`,
    );
  }

  const srcIdents = collectIdents(query).map((p) => p.join('.'));
  const dstIdents = collectParsedIdents(parsed).map((p) => p.join('.'));
  if (srcIdents.length !== dstIdents.length) {
    problems.push(
      `identifier count ${srcIdents.length} != parsed identifier count ${dstIdents.length}`,
    );
  } else {
    for (let i = 0; i < srcIdents.length; i++) {
      if (srcIdents[i] !== dstIdents[i]) {
        problems.push(
          `identifier ${i} did not round-trip: ${JSON.stringify(srcIdents[i])} -> ${JSON.stringify(dstIdents[i])}`,
        );
      }
    }
  }

  return {
    ok: problems.length === 0,
    problems,
    sql: rendered.sql,
    bindings: rendered.bindings,
  };
}
