/**
 * 模板 AST 定义。
 *
 * 模板能表达的东西被刻意限制为：文本、变量、字段索引、条件、
 * 固定列表循环和宏调用。没有算术、没有函数值、没有动态属性名，
 * 因此模板本身无法构造宿主对象或执行任意计算。
 */

/** 标量：模板数据里唯一允许直接出现的值。 */
export type Scalar = string | number | boolean | null;

/** 净化后的数据值：标量、只读数组、无原型只读记录。 */
export type DataValue = Scalar | readonly DataValue[] | DataRecord;

/** 无原型、冻结的数据记录。 */
export type DataRecord = Readonly<Record<string, DataValue>>;

/** 表达式：求值产生一个 DataValue。 */
export type Expr =
  | { readonly kind: 'lit'; readonly value: Scalar }
  | { readonly kind: 'var'; readonly name: string }
  | { readonly kind: 'field'; readonly target: Expr; readonly key: string }
  | { readonly kind: 'list'; readonly items: readonly Expr[] };

/**
 * 模板节点。`var` / `field` / `lit` 表达式直接作为节点出现时，
 * 其求值结果会被渲染到输出。
 */
export type Node =
  | { readonly kind: 'text'; readonly value: string }
  | Expr
  | {
      readonly kind: 'if';
      readonly cond: Expr;
      readonly then: readonly Node[];
      readonly else?: readonly Node[];
    }
  | {
      readonly kind: 'for';
      readonly name: string;
      readonly list: Expr;
      readonly body: readonly Node[];
    }
  | { readonly kind: 'call'; readonly name: string; readonly args: readonly Expr[] };

/** 宏定义：参数名 + 节点体。宏在定义处捕获词法环境。 */
export interface MacroDef {
  readonly name: string;
  readonly params: readonly string[];
  readonly body: readonly Node[];
}

/** 模板 = 顶层宏定义集合 + 主体节点序列。 */
export interface Template {
  readonly macros?: readonly MacroDef[];
  readonly body: readonly Node[];
}

/** 解释器预算。所有限制都在求值前/求值中强制，超限即失败。 */
export interface Budgets {
  /** 宏调用的最大嵌套深度（含自递归）。 */
  readonly maxCallDepth: number;
  /** 输出最大字节数（UTF-8 编码后）。 */
  readonly maxOutputBytes: number;
  /** 所有循环的总迭代次数上限，防止恶意数据里的超大数组。 */
  readonly maxIterations: number;
  /** 净化不可信数据时允许的最大节点数。 */
  readonly maxDataNodes: number;
  /** 净化不可信数据时允许的最大嵌套深度。 */
  readonly maxDataDepth: number;
}

export const DEFAULT_BUDGETS: Budgets = {
  maxCallDepth: 64,
  maxOutputBytes: 1 << 20,
  maxIterations: 10_000,
  maxDataNodes: 10_000,
  maxDataDepth: 32,
};
