/**
 * Renders a QueryNode to SQL plus a positional params array.
 *
 * Placeholder numbering is deterministic: nodes are visited in a fixed
 * traversal order (SELECT, FROM subquery, WHERE depth-first left-to-right,
 * ORDER BY, LIMIT), and each distinct ParamNode instance is assigned the
 * next number the first time it is encountered. Re-encountering the same
 * instance reuses its number; equal values in distinct instances are never
 * merged.
 */
import {
  ComparisonNode,
  InListNode,
  InSubqueryNode,
  FieldNode,
  ParamNode,
  QueryNode,
} from './ast.ts';
import type { ConditionNode, OperandNode } from './ast.ts';

export interface RenderedQuery {
  sql: string;
  params: unknown[];
}

export function quoteIdent(name: string): string {
  if (name === '*') return '*';
  return '"' + name.replace(/"/g, '""') + '"';
}

export function renderOperand(node: OperandNode): string {
  if (node instanceof FieldNode) {
    return node.path.map(quoteIdent).join('.');
  }
  return node.text;
}

class Renderer {
  private readonly ids = new Map<ParamNode, number>();
  readonly params: unknown[] = [];

  placeholder(node: ParamNode): string {
    const existing = this.ids.get(node);
    if (existing !== undefined) {
      return '$' + existing;
    }
    const id = this.params.length + 1;
    this.ids.set(node, id);
    this.params.push(node.value);
    return '$' + id;
  }

  query(q: QueryNode): string {
    if (!q.source) {
      throw new Error('query has no FROM clause');
    }
    const parts: string[] = [];
    parts.push('SELECT ' + q.selectItems.map(renderOperand).join(', '));
    const source = q.source;
    if (source.kind === 'table') {
      parts.push('FROM ' + renderOperand(source.table));
    } else {
      parts.push('FROM (' + this.query(source.query) + ') AS ' + quoteIdent(source.alias));
    }
    if (q.condition) {
      parts.push('WHERE ' + this.condition(q.condition));
    }
    if (q.orderItems.length > 0) {
      parts.push(
        'ORDER BY ' + q.orderItems.map((item) => renderOperand(item.expr) + ' ' + item.dir).join(', '),
      );
    }
    if (q.limitParam) {
      parts.push('LIMIT ' + this.placeholder(q.limitParam));
    }
    return parts.join(' ');
  }

  condition(node: ConditionNode): string {
    if (node instanceof ComparisonNode) {
      return renderOperand(node.left) + ' ' + node.op + ' ' + this.placeholder(node.right);
    }
    if (node instanceof InListNode) {
      if (node.params.length === 0) {
        return node.negated ? '(1 = 1)' : '(1 = 0)';
      }
      const placeholders = node.params.map((p) => this.placeholder(p)).join(', ');
      return renderOperand(node.left) + (node.negated ? ' NOT IN (' : ' IN (') + placeholders + ')';
    }
    if (node instanceof InSubqueryNode) {
      return (
        renderOperand(node.left) +
        (node.negated ? ' NOT IN (' : ' IN (') +
        this.query(node.query) +
        ')'
      );
    }
    if (node.children.length === 0) {
      return node.op === 'and' ? '(1 = 1)' : '(1 = 0)';
    }
    const sep = node.op === 'and' ? ' AND ' : ' OR ';
    return '(' + node.children.map((child) => this.condition(child)).join(sep) + ')';
  }
}

export function render(query: QueryNode): RenderedQuery {
  const renderer = new Renderer();
  const sql = renderer.query(query);
  return { sql, params: renderer.params };
}
