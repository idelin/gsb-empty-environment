import type { Expression } from './expression.ts';

/** 输出列：一个名字 + 产出它的表达式 */
export interface OutputColumn {
  name: string;
  expr: Expression;
}

/**
 * 逻辑计划算子节点。
 * 每个算子暴露 outputSchema()（输出字段名列表），
 * 并由修剪器通过 applyRequiredColumns() 自顶向下驱动裁剪。
 */
export abstract class Operator {
  /** 当前输出模式（字段名，有序） */
  abstract outputSchema(): string[];

  /**
   * 修剪器入口：parentRequired 是上层算子真正消费的字段集合。
   * 算子据此裁剪自身，并把依赖继续向下传递。
   */
  abstract applyRequiredColumns(parentRequired: ReadonlySet<string>): void;

  abstract describe(indent?: string): string;
}

/** 叶子节点：表扫描。schema 为表中全部可用列，可被裁剪。 */
export class Scan extends Operator {
  readonly table: string;
  readonly fullSchema: string[];
  /** 裁剪后实际读取的列（初始为全表） */
  private projectedSchema: string[];

  constructor(table: string, fullSchema: string[]) {
    super();
    this.table = table;
    this.fullSchema = fullSchema;
    this.projectedSchema = [...fullSchema];
  }

  outputSchema(): string[] {
    return [...this.projectedSchema];
  }

  applyRequiredColumns(parentRequired: ReadonlySet<string>): void {
    // 只保留上层真正需要的列；未知列直接忽略（保持健壮）
    this.projectedSchema = this.fullSchema.filter((c) => parentRequired.has(c));
  }

  describe(indent = ''): string {
    return `${indent}Scan(${this.table}) -> [${this.projectedSchema.join(', ')}]`;
  }
}

/** 投影：按表达式列表产出新列 */
export class Project extends Operator {
  columns: OutputColumn[];
  readonly child: Operator;

  constructor(columns: OutputColumn[], child: Operator) {
    super();
    this.columns = columns;
    this.child = child;
  }

  outputSchema(): string[] {
    return this.columns.map((c) => c.name);
  }

  applyRequiredColumns(parentRequired: ReadonlySet<string>): void {
    // 1. 裁剪掉上层不消费的输出列
    this.columns = this.columns.filter((c) => parentRequired.has(c.name));
    // 2. 收集保留下来的表达式所引用的输入字段，向下传递
    const needed = new Set<string>();
    for (const col of this.columns) {
      for (const f of col.expr.referencedFields()) needed.add(f);
    }
    this.child.applyRequiredColumns(needed);
  }

  describe(indent = ''): string {
    const cols = this.columns.map((c) => `${c.name}=${c.expr}`).join(', ');
    return `${indent}Project[${cols}]\n${this.child.describe(indent + '  ')}`;
  }
}

/** 过滤：谓词引用的字段必须额外向下请求 */
export class Filter extends Operator {
  readonly predicate: Expression;
  readonly child: Operator;

  constructor(predicate: Expression, child: Operator) {
    super();
    this.predicate = predicate;
    this.child = child;
  }

  outputSchema(): string[] {
    return this.child.outputSchema();
  }

  applyRequiredColumns(parentRequired: ReadonlySet<string>): void {
    const needed = new Set(parentRequired);
    for (const f of this.predicate.referencedFields()) needed.add(f);
    this.child.applyRequiredColumns(needed);
  }

  describe(indent = ''): string {
    return `${indent}Filter[${this.predicate}]\n${this.child.describe(indent + '  ')}`;
  }
}

/** 等值连接：把上层需求按左右两侧拆分，并补上连接键 */
export class Join extends Operator {
  readonly left: Operator;
  readonly right: Operator;
  readonly leftKeys: string[];
  readonly rightKeys: string[];

  constructor(left: Operator, right: Operator, leftKeys: string[], rightKeys: string[]) {
    super();
    if (leftKeys.length !== rightKeys.length) {
      throw new Error('join key count mismatch');
    }
    this.left = left;
    this.right = right;
    this.leftKeys = leftKeys;
    this.rightKeys = rightKeys;
  }

  outputSchema(): string[] {
    return [...this.left.outputSchema(), ...this.right.outputSchema()];
  }

  applyRequiredColumns(parentRequired: ReadonlySet<string>): void {
    const leftCols = new Set(this.left.outputSchema());
    const rightCols = new Set(this.right.outputSchema());

    const leftNeeded = new Set<string>();
    const rightNeeded = new Set<string>();
    for (const col of parentRequired) {
      if (leftCols.has(col)) leftNeeded.add(col);
      else if (rightCols.has(col)) rightNeeded.add(col);
      // 两侧都没有的列（例如上层引用错误）直接忽略
    }
    // 连接键本身必须保留，否则无法完成 Join
    for (const k of this.leftKeys) leftNeeded.add(k);
    for (const k of this.rightKeys) rightNeeded.add(k);

    this.left.applyRequiredColumns(leftNeeded);
    this.right.applyRequiredColumns(rightNeeded);
  }

  describe(indent = ''): string {
    const cond = this.leftKeys.map((k, i) => `${k}=${this.rightKeys[i]}`).join(' AND ');
    return (
      `${indent}Join[${cond}]\n` +
      `${this.left.describe(indent + '  L: ')}\n` +
      `${this.right.describe(indent + '  R: ')}`
    );
  }
}
