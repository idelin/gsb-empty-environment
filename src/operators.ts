import { type Ordering, formatOrdering } from './ordering.ts';

/**
 * Physical plan operators. Each operator declares:
 *  - providedOrdering(): the ordering its output stream guarantees
 *  - requiredChildOrderings(): the physical ordering it requires from
 *    each of its inputs (null = no requirement)
 */
export abstract class PhysicalNode {
  children: PhysicalNode[] = [];

  abstract get name(): string;

  abstract providedOrdering(): Ordering;

  requiredChildOrderings(): (Ordering | null)[] {
    return this.children.map(() => null);
  }

  protected detail(): string {
    return '';
  }

  describe(indent: string = ''): string {
    const header = `${indent}${this.name}${this.detail()}`;
    const subs = this.children.map((c) => c.describe(`${indent}  `));
    return [header, ...subs].join('\n');
  }
}

/** Full table scan: delivers rows in arbitrary (unordered) sequence. */
export class TableScan extends PhysicalNode {
  readonly table: string;

  constructor(table: string) {
    super();
    this.table = table;
  }

  get name(): string {
    return 'TableScan';
  }

  protected detail(): string {
    return `(${this.table})`;
  }

  providedOrdering(): Ordering {
    return [];
  }
}

/** Index scan: delivers rows in index key order — free physical ordering. */
export class IndexScan extends PhysicalNode {
  readonly table: string;
  readonly indexName: string;
  readonly indexOrder: Ordering;

  constructor(table: string, indexName: string, indexOrder: Ordering) {
    super();
    this.table = table;
    this.indexName = indexName;
    this.indexOrder = indexOrder;
  }

  get name(): string {
    return 'IndexScan';
  }

  protected detail(): string {
    return `(${this.table} USING ${this.indexName} -> ${formatOrdering(this.indexOrder)})`;
  }

  providedOrdering(): Ordering {
    return this.indexOrder;
  }
}

/** Sort enforcer: inserted by the optimizer only when no cheaper input satisfies the requirement. */
export class Sort extends PhysicalNode {
  readonly input: PhysicalNode;
  readonly orderBy: Ordering;

  constructor(input: PhysicalNode, orderBy: Ordering) {
    super();
    this.input = input;
    this.orderBy = orderBy;
    this.children = [input];
  }

  get name(): string {
    return 'Sort';
  }

  protected detail(): string {
    return `(ORDER BY ${formatOrdering(this.orderBy)})`;
  }

  providedOrdering(): Ordering {
    return this.orderBy;
  }
}

/** Streaming (ordered) GroupBy: requires input sorted on the grouping keys. */
export class GroupBy extends PhysicalNode {
  readonly input: PhysicalNode;
  readonly groupKeys: Ordering;

  constructor(input: PhysicalNode, groupKeys: Ordering) {
    super();
    this.input = input;
    this.groupKeys = groupKeys;
    this.children = [input];
  }

  get name(): string {
    return 'GroupBy';
  }

  protected detail(): string {
    return `(KEYS ${formatOrdering(this.groupKeys)})`;
  }

  providedOrdering(): Ordering {
    return this.groupKeys;
  }

  requiredChildOrderings(): (Ordering | null)[] {
    return [this.groupKeys];
  }
}

/** MergeJoin: requires both inputs sorted on their join keys. */
export class MergeJoin extends PhysicalNode {
  readonly left: PhysicalNode;
  readonly right: PhysicalNode;
  readonly leftKeys: Ordering;
  readonly rightKeys: Ordering;

  constructor(
    left: PhysicalNode,
    right: PhysicalNode,
    leftKeys: Ordering,
    rightKeys: Ordering,
  ) {
    super();
    this.left = left;
    this.right = right;
    this.leftKeys = leftKeys;
    this.rightKeys = rightKeys;
    this.children = [left, right];
  }

  get name(): string {
    return 'MergeJoin';
  }

  protected detail(): string {
    return `(LEFT ${formatOrdering(this.leftKeys)} = RIGHT ${formatOrdering(this.rightKeys)})`;
  }

  providedOrdering(): Ordering {
    return this.leftKeys;
  }

  requiredChildOrderings(): (Ordering | null)[] {
    return [this.leftKeys, this.rightKeys];
  }
}
