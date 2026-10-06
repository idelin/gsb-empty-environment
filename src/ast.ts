/**
 * Query AST nodes.
 *
 * Binding semantics: values are bound by *parameter node identity*.
 * - Two ParamNode instances holding equal values are NOT merged; each gets
 *   its own placeholder.
 * - The same ParamNode instance appearing multiple times in a query gets a
 *   single placeholder number, and its value appears once in the params list.
 */
export type ComparisonOp = '=' | '<>' | '!=' | '<' | '<=' | '>' | '>=' | 'LIKE';

export class ParamNode<T = unknown> {
  readonly kind = 'param';
  readonly value: T;
  constructor(value: T) {
    this.value = value;
  }
}

export class FieldNode {
  readonly kind = 'field';
  readonly path: readonly string[];
  constructor(path: readonly string[]) {
    if (path.length === 0 || path.some((part) => part.length === 0)) {
      throw new Error('field path must be non-empty');
    }
    this.path = path;
  }
}

/**
 * Raw SQL fragment. Raw text is emitted verbatim and therefore must not
 * carry values: any placeholder syntax (`?` or `$n`) is rejected, and the
 * node has no slot for bound values.
 */
export class RawNode {
  readonly kind = 'raw';
  readonly text: string;
  constructor(text: string) {
    if (text.trim().length === 0) {
      throw new Error('raw SQL fragment must be non-empty');
    }
    if (/\?|\$\d+/.test(text)) {
      throw new Error(
        `raw SQL fragments cannot contain value placeholders: ${JSON.stringify(text)}`,
      );
    }
    this.text = text;
  }
}

export type OperandNode = FieldNode | RawNode;
export type SelectItem = OperandNode;

export class ComparisonNode {
  readonly kind = 'cmp';
  readonly left: OperandNode;
  readonly op: ComparisonOp;
  readonly right: ParamNode;
  constructor(left: OperandNode, op: ComparisonOp, right: ParamNode) {
    this.left = left;
    this.op = op;
    this.right = right;
  }
}

/** `left IN (v1, v2, ...)`; an empty list renders as a constant false/true. */
export class InListNode {
  readonly kind = 'inList';
  readonly left: OperandNode;
  readonly params: readonly ParamNode[];
  readonly negated: boolean;
  constructor(left: OperandNode, params: readonly ParamNode[], negated = false) {
    this.left = left;
    this.params = params;
    this.negated = negated;
  }
}

export class InSubqueryNode {
  readonly kind = 'inSub';
  readonly left: OperandNode;
  readonly query: QueryNode;
  readonly negated: boolean;
  constructor(left: OperandNode, query: QueryNode, negated = false) {
    this.left = left;
    this.query = query;
    this.negated = negated;
  }
}

export class JunctionNode {
  readonly kind = 'junction';
  readonly op: 'and' | 'or';
  readonly children: readonly ConditionNode[];
  constructor(op: 'and' | 'or', children: readonly ConditionNode[]) {
    this.op = op;
    this.children = children;
  }
}

export type ConditionNode = ComparisonNode | InListNode | InSubqueryNode | JunctionNode;

export type FromSource =
  | { kind: 'table'; table: FieldNode }
  | { kind: 'subquery'; query: QueryNode; alias: string };

export interface OrderItem {
  expr: OperandNode;
  dir: 'ASC' | 'DESC';
}

export class QueryNode {
  readonly kind = 'query';
  readonly selectItems: readonly SelectItem[];
  source: FromSource | undefined;
  condition: ConditionNode | undefined;
  readonly orderItems: OrderItem[] = [];
  limitParam: ParamNode | undefined;

  constructor(selectItems: readonly SelectItem[]) {
    if (selectItems.length === 0) {
      throw new Error('query must select at least one item');
    }
    this.selectItems = selectItems;
  }

  from(table: string): this {
    this.source = { kind: 'table', table: field(table) };
    return this;
  }

  fromSubquery(subquery: QueryNode, alias: string): this {
    if (alias.length === 0) {
      throw new Error('subquery alias must be non-empty');
    }
    this.source = { kind: 'subquery', query: subquery, alias };
    return this;
  }

  where(condition: ConditionNode): this {
    this.condition = condition;
    return this;
  }

  orderBy(expr: OperandNode, dir: 'ASC' | 'DESC' = 'ASC'): this {
    this.orderItems.push({ expr, dir });
    return this;
  }

  limit(limitParam: ParamNode): this {
    this.limitParam = limitParam;
    return this;
  }
}

export function param<T>(value: T): ParamNode<T> {
  return new ParamNode(value);
}

export function field(name: string): FieldNode {
  return new FieldNode(name.split('.'));
}

export function raw(text: string): RawNode {
  return new RawNode(text);
}

export function select(...items: readonly SelectItem[]): QueryNode {
  return new QueryNode(items);
}

export function cmp(left: OperandNode, op: ComparisonOp, right: ParamNode): ComparisonNode {
  return new ComparisonNode(left, op, right);
}

export function eq(left: OperandNode, right: ParamNode): ComparisonNode {
  return new ComparisonNode(left, '=', right);
}

export function and(...children: readonly ConditionNode[]): JunctionNode {
  return new JunctionNode('and', children);
}

export function or(...children: readonly ConditionNode[]): JunctionNode {
  return new JunctionNode('or', children);
}

export function inList(
  left: OperandNode,
  params: readonly ParamNode[],
  negated = false,
): InListNode {
  return new InListNode(left, params, negated);
}

export function notInList(left: OperandNode, params: readonly ParamNode[]): InListNode {
  return new InListNode(left, params, true);
}

export function inSubquery(left: OperandNode, query: QueryNode, negated = false): InSubqueryNode {
  return new InSubqueryNode(left, query, negated);
}
