/**
 * 表达式模型：仅用于字段依赖分析。
 * 每个表达式都能报告自己引用了哪些输入字段。
 */
export interface Expression {
  /** 该表达式引用到的输入字段名集合 */
  referencedFields(): Set<string>;
  /** 展示用 */
  toString(): string;
}

/** 字段引用，例如 users.id */
export class FieldRef implements Expression {
  readonly field: string;

  constructor(field: string) {
    this.field = field;
  }

  referencedFields(): Set<string> {
    return new Set([this.field]);
  }

  toString(): string {
    return this.field;
  }
}

/** 字面量常量，不引用任何字段 */
export class Literal implements Expression {
  readonly value: unknown;

  constructor(value: unknown) {
    this.value = value;
  }

  referencedFields(): Set<string> {
    return new Set();
  }

  toString(): string {
    return JSON.stringify(this.value);
  }
}

/** 二元运算，例如 a + b、a = b */
export class BinaryExpr implements Expression {
  readonly op: string;
  readonly left: Expression;
  readonly right: Expression;

  constructor(op: string, left: Expression, right: Expression) {
    this.op = op;
    this.left = left;
    this.right = right;
  }

  referencedFields(): Set<string> {
    return new Set([...this.left.referencedFields(), ...this.right.referencedFields()]);
  }

  toString(): string {
    return `(${this.left} ${this.op} ${this.right})`;
  }
}

/** 函数调用，例如 upper(name) */
export class FuncCall implements Expression {
  readonly name: string;
  readonly args: Expression[];

  constructor(name: string, args: Expression[]) {
    this.name = name;
    this.args = args;
  }

  referencedFields(): Set<string> {
    const fields = new Set<string>();
    for (const arg of this.args) {
      for (const f of arg.referencedFields()) fields.add(f);
    }
    return fields;
  }

  toString(): string {
    return `${this.name}(${this.args.map(String).join(', ')})`;
  }
}
