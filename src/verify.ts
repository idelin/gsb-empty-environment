/**
 * Render-then-reparse verification: renders a query, parses the SQL back,
 * and checks that every placeholder corresponds to the right parameter node
 * and bound value, following the same deterministic traversal order as the
 * renderer (SELECT, FROM subquery, WHERE depth-first, ORDER BY, LIMIT).
 */
import {
  ComparisonNode,
  InListNode,
  InSubqueryNode,
  ParamNode,
  QueryNode,
} from './ast.ts';
import type { ConditionNode } from './ast.ts';
import { render } from './render.ts';
import { parse } from './parse.ts';
import type { PCondition, PQuery } from './parse.ts';

export function collectParamNodes(query: QueryNode): ParamNode[] {
  const out: ParamNode[] = [];
  const walkCondition = (node: ConditionNode): void => {
    if (node instanceof ComparisonNode) {
      out.push(node.right);
    } else if (node instanceof InListNode) {
      out.push(...node.params);
    } else if (node instanceof InSubqueryNode) {
      walkQuery(node.query);
    } else {
      node.children.forEach(walkCondition);
    }
  };
  const walkQuery = (node: QueryNode): void => {
    const source = node.source;
    if (source && source.kind === 'subquery') walkQuery(source.query);
    if (node.condition) walkCondition(node.condition);
    if (node.limitParam) out.push(node.limitParam);
  };
  walkQuery(query);
  return out;
}

export function collectPlaceholders(query: PQuery): number[] {
  const out: number[] = [];
  const walkCondition = (node: PCondition): void => {
    switch (node.kind) {
      case 'cmp':
        out.push(node.placeholder);
        break;
      case 'inList':
        out.push(...node.placeholders);
        break;
      case 'inSub':
        walkQuery(node.query);
        break;
      case 'and':
      case 'or':
        node.children.forEach(walkCondition);
        break;
      case 'const':
        break;
    }
  };
  const walkQuery = (node: PQuery): void => {
    if (node.from.kind === 'subquery') walkQuery(node.from.query);
    if (node.where) walkCondition(node.where);
    if (node.limit !== undefined) out.push(node.limit);
  };
  walkQuery(query);
  return out;
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (a instanceof Date && b instanceof Date) return a.getTime() === b.getTime();
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((item, i) => deepEqual(item, b[i]));
  }
  if (a && b && typeof a === 'object' && typeof b === 'object') {
    const keysA = Object.keys(a);
    const keysB = Object.keys(b);
    return (
      keysA.length === keysB.length &&
      keysA.every((key) =>
        deepEqual((a as Record<string, unknown>)[key], (b as Record<string, unknown>)[key]),
      )
    );
  }
  return false;
}

export interface BindingRecord {
  position: number;
  placeholder: number;
  value: unknown;
  bound: unknown;
  ok: boolean;
}

export interface VerifyReport {
  ok: boolean;
  sql: string;
  params: unknown[];
  bindings: BindingRecord[];
  errors: string[];
}

export function verifyRoundTrip(query: QueryNode): VerifyReport {
  const errors: string[] = [];
  const { sql, params } = render(query);
  const parsed = parse(sql);
  const nodes = collectParamNodes(query);
  const holes = collectPlaceholders(parsed);

  if (nodes.length !== holes.length) {
    errors.push(`placeholder count ${holes.length} != param node count ${nodes.length}`);
  }

  const bindings: BindingRecord[] = [];
  const count = Math.min(nodes.length, holes.length);
  for (let i = 0; i < count; i++) {
    const placeholder = holes[i];
    const inRange = placeholder >= 1 && placeholder <= params.length;
    const bound = inRange ? params[placeholder - 1] : undefined;
    const ok = inRange && deepEqual(bound, nodes[i].value);
    if (!ok) {
      errors.push(
        `position ${i}: placeholder $${placeholder} does not bind the expected value`,
      );
    }
    bindings.push({ position: i, placeholder, value: nodes[i].value, bound, ok });
  }

  const numberByNode = new Map<ParamNode, number>();
  const nodeByNumber = new Map<number, ParamNode>();
  for (let i = 0; i < count; i++) {
    const node = nodes[i];
    const placeholder = holes[i];
    const previous = numberByNode.get(node);
    if (previous !== undefined && previous !== placeholder) {
      errors.push(
        `same param node assigned multiple placeholders ($${previous} and $${placeholder})`,
      );
    }
    numberByNode.set(node, placeholder);
    const owner = nodeByNumber.get(placeholder);
    if (owner !== undefined && owner !== node) {
      errors.push(`placeholder $${placeholder} shared by distinct param nodes`);
    }
    nodeByNumber.set(placeholder, node);
  }

  if (numberByNode.size !== params.length) {
    errors.push(`params length ${params.length} != distinct param nodes ${numberByNode.size}`);
  }
  for (let n = 1; n <= params.length; n++) {
    if (!nodeByNumber.has(n)) {
      errors.push(`placeholder $${n} is never used in the SQL`);
    }
  }

  return { ok: errors.length === 0, sql, params, bindings, errors };
}
