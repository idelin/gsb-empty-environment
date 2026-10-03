import type { Schema } from "./schema.ts";
import { fieldNames } from "./schema.ts";

/**
 * 查询计划算子节点。
 * 修剪器自顶向下遍历时，会向每个算子询问：
 *  1. 它自身需要消费子算子的哪些字段（join key、谓词引用等）；
 *  2. 它的输出模式（供上层按名匹配字段来源）。
 */
export abstract class Operator {
  readonly kind: string;
  readonly children: Operator[] = [];
  /** 算子当前的输出模式（修剪后会被重写）。 */
  outputSchema: Schema;

  protected constructor(kind: string, outputSchema: Schema) {
    this.kind = kind;
    this.outputSchema = outputSchema;
  }

  /** 本算子自身逻辑需要从第 childIndex 个孩子处读取的字段名。 */
  abstract localRequiredFields(childIndex: number): ReadonlySet<string>;

  /** 子算子被修剪后，根据孩子最新的输出模式重算自身输出模式。 */
  abstract refreshOutputSchema(): void;
}

/** 叶子节点：表扫描。修剪的终点，直接压缩其输出模式。 */
export class Scan extends Operator {
  readonly tableName: string;

  constructor(tableName: string, schema: Schema) {
    super("Scan", schema.map((f) => ({ ...f })));
    this.tableName = tableName;
  }

  localRequiredFields(): ReadonlySet<string> {
    return new Set();
  }

  refreshOutputSchema(): void {
    // Scan 是叶子，输出模式由修剪器直接裁剪，无需重算。
  }
}

/** 投影表达式：输出列名 + 引用的输入字段。 */
export interface ProjectionExpr {
  /** 输出列名（上层按此名消费）。 */
  name: string;
  /** 表达式引用到的下层字段。 */
  refs: string[];
}

/** 投影算子：输出列由表达式列表决定。 */
export class Project extends Operator {
  expressions: ProjectionExpr[];

  constructor(child: Operator, expressions: ProjectionExpr[]) {
    super("Project", []);
    this.expressions = expressions;
    this.children.push(child);
    this.refreshOutputSchema();
  }

  localRequiredFields(): ReadonlySet<string> {
    const refs = new Set<string>();
    for (const expr of this.expressions) {
      for (const ref of expr.refs) refs.add(ref);
    }
    return refs;
  }

  refreshOutputSchema(): void {
    const childSchema = this.children[0].outputSchema;
    this.outputSchema = this.expressions.map((expr) => {
      const source = childSchema.find((f) => f.name === expr.refs[0]);
      return { name: expr.name, type: source?.type ?? "UNKNOWN" };
    });
  }
}

/** 过滤算子：谓词引用的字段必须保留，其余字段透传上层需求。 */
export class Filter extends Operator {
  readonly predicateRefs: string[];

  constructor(child: Operator, predicateRefs: string[]) {
    super("Filter", []);
    this.predicateRefs = predicateRefs;
    this.children.push(child);
    this.refreshOutputSchema();
  }

  localRequiredFields(): ReadonlySet<string> {
    return new Set(this.predicateRefs);
  }

  refreshOutputSchema(): void {
    this.outputSchema = this.children[0].outputSchema.map((f) => ({ ...f }));
  }
}

/** 等值连接算子：左/右各自携带一组 join key。 */
export class Join extends Operator {
  readonly leftKeys: string[];
  readonly rightKeys: string[];

  constructor(left: Operator, right: Operator, leftKeys: string[], rightKeys: string[]) {
    super("Join", []);
    this.leftKeys = leftKeys;
    this.rightKeys = rightKeys;
    this.children.push(left, right);
    this.refreshOutputSchema();
  }

  localRequiredFields(childIndex: number): ReadonlySet<string> {
    return new Set(childIndex === 0 ? this.leftKeys : this.rightKeys);
  }

  refreshOutputSchema(): void {
    this.outputSchema = this.children.flatMap((c) =>
      c.outputSchema.map((f) => ({ ...f })),
    );
  }
}

export function outputNames(op: Operator): string[] {
  return fieldNames(op.outputSchema);
}
