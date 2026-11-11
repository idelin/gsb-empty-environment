import {
  DEFAULT_BUDGETS,
  type Budgets,
  type DataValue,
  type Expr,
  type MacroDef,
  type Node,
  type Template,
} from './ast.ts';
import { BudgetExceededError, TemplateError } from './errors.ts';
import { isDataRecord, sanitizeData } from './sanitize.ts';

/** 宏闭包：定义体 + 定义处的词法环境。调用时绝不引入调用方环境。 */
interface MacroClosure {
  readonly def: MacroDef;
  readonly defEnv: Env;
}

type Binding =
  | { readonly tag: 'value'; readonly value: DataValue }
  | { readonly tag: 'macro'; readonly macro: MacroClosure };

/**
 * 词法环境帧链。帧用 Map 实现，变量名永远不会与宿主对象
 * 原型键（__proto__ 等）发生任何关系。
 */
class Env {
  private readonly frame = new Map<string, Binding>();
  readonly parent: Env | null;

  constructor(parent: Env | null) {
    this.parent = parent;
  }

  define(name: string, binding: Binding): void {
    this.frame.set(name, binding);
  }

  lookup(name: string): Binding | undefined {
    let env: Env | null = this;
    while (env !== null) {
      const hit = env.frame.get(name);
      if (hit !== undefined) return hit;
      env = env.parent;
    }
    return undefined;
  }
}

const textEncoder = new TextEncoder();

class Interpreter {
  private readonly budgets: Budgets;
  private chunks: string[] = [];
  private outputBytes = 0;
  private callDepth = 0;
  private iterations = 0;

  constructor(budgets: Budgets) {
    this.budgets = budgets;
  }

  run(template: Template, globalEnv: Env): string {
    const env = new Env(globalEnv);
    for (const macro of template.macros ?? []) {
      if (env.lookup(macro.name) !== undefined) {
        throw new TemplateError(
          'DUPLICATE_MACRO',
          `duplicate macro or name conflict: "${macro.name}"`,
        );
      }
      // 关键：闭包捕获的是定义处环境 env（宏可见全局与彼此），
      // 而不是未来任何调用点的环境。
      env.define(macro.name, { tag: 'macro', macro: { def: macro, defEnv: env } });
    }
    this.execNodes(template.body, env);
    const output = this.chunks.join('');
    // 成功后才能拿到输出；失败路径上 chunks 随解释器实例一起被丢弃。
    this.chunks = [];
    return output;
  }

  private emit(value: DataValue): void {
    let text: string;
    if (value === null) {
      text = '';
    } else if (typeof value === 'string') {
      text = value;
    } else if (typeof value === 'number' || typeof value === 'boolean') {
      text = String(value);
    } else {
      throw new TemplateError(
        'TYPE_MISMATCH',
        'cannot render a non-scalar value',
      );
    }
    this.outputBytes += textEncoder.encode(text).byteLength;
    if (this.outputBytes > this.budgets.maxOutputBytes) {
      throw new BudgetExceededError(
        'OUTPUT_BUDGET_EXCEEDED',
        `output exceeds ${this.budgets.maxOutputBytes} bytes`,
      );
    }
    this.chunks.push(text);
  }

  private evalExpr(expr: Expr, env: Env): DataValue {
    switch (expr.kind) {
      case 'lit':
        return expr.value;
      case 'var': {
        const binding = env.lookup(expr.name);
        if (binding === undefined) {
          throw new TemplateError(
            'UNKNOWN_NAME',
            `unknown name: "${expr.name}"`,
          );
        }
        if (binding.tag !== 'value') {
          throw new TemplateError(
            'TYPE_MISMATCH',
            `"${expr.name}" is a macro, not a value`,
          );
        }
        return binding.value;
      }
      case 'field': {
        const target = this.evalExpr(expr.target, env);
        return this.readField(target, expr.key);
      }
      case 'list': {
        const items = expr.items.map((item) => this.evalExpr(item, env));
        return Object.freeze(items) as readonly DataValue[];
      }
    }
  }

  private readField(target: DataValue, key: string): DataValue {
    if (isDataRecord(target)) {
      // 记录是无原型冻结对象，只认自有键；缺失即错误，绝不回退到原型链。
      if (Object.prototype.hasOwnProperty.call(target, key)) {
        return target[key];
      }
      throw new TemplateError(
        'UNKNOWN_FIELD',
        `record has no field "${key}"`,
      );
    }
    if (Array.isArray(target)) {
      if (key === 'length') return target.length;
      if (/^(0|[1-9]\d*)$/.test(key)) {
        const index = Number(key);
        if (index < target.length) return target[index] as DataValue;
      }
      throw new TemplateError(
        'UNKNOWN_FIELD',
        `array has no element "${key}"`,
      );
    }
    throw new TemplateError(
      'TYPE_MISMATCH',
      `cannot index field "${key}" of a scalar`,
    );
  }

  private static isTruthy(value: DataValue): boolean {
    return value !== null && value !== false && value !== 0 && value !== '';
  }

  private execNodes(nodes: readonly Node[], env: Env): void {
    for (const node of nodes) {
      this.execNode(node, env);
    }
  }

  private execNode(node: Node, env: Env): void {
    switch (node.kind) {
      case 'text':
        this.emit(node.value);
        return;
      case 'lit':
      case 'var':
      case 'field':
        this.emit(this.evalExpr(node, env));
        return;
      case 'list':
        throw new TemplateError(
          'TYPE_MISMATCH',
          'cannot render a list literal',
        );
      case 'if': {
        const cond = this.evalExpr(node.cond, env);
        const branch = Interpreter.isTruthy(cond)
          ? node.then
          : (node.else ?? []);
        this.execNodes(branch, env);
        return;
      }
      case 'for': {
        const list = this.evalExpr(node.list, env);
        if (!Array.isArray(list)) {
          throw new TemplateError(
            'TYPE_MISMATCH',
            'for loop requires a list',
          );
        }
        for (const item of list as readonly DataValue[]) {
          this.iterations += 1;
          if (this.iterations > this.budgets.maxIterations) {
            throw new BudgetExceededError(
              'ITERATION_BUDGET_EXCEEDED',
              `loop iterations exceed ${this.budgets.maxIterations}`,
            );
          }
          // 每次迭代新建帧：循环变量遮蔽外层同名绑定，且不外泄。
          const loopEnv = new Env(env);
          loopEnv.define(node.name, { tag: 'value', value: item });
          this.execNodes(node.body, loopEnv);
        }
        return;
      }
      case 'call': {
        const binding = env.lookup(node.name);
        if (binding === undefined) {
          throw new TemplateError(
            'UNKNOWN_NAME',
            `unknown macro: "${node.name}"`,
          );
        }
        if (binding.tag !== 'macro') {
          throw new TemplateError(
            'NOT_A_MACRO',
            `"${node.name}" is not a macro`,
          );
        }
        const { def, defEnv } = binding.macro;
        if (node.args.length !== def.params.length) {
          throw new TemplateError(
            'ARITY_MISMATCH',
            `macro "${def.name}" expects ${def.params.length} args, got ${node.args.length}`,
          );
        }
        this.callDepth += 1;
        if (this.callDepth > this.budgets.maxCallDepth) {
          throw new BudgetExceededError(
            'DEPTH_BUDGET_EXCEEDED',
            `macro call depth exceeds ${this.budgets.maxCallDepth}`,
          );
        }
        try {
          // 实参在调用方环境求值（按值传递）……
          const args = node.args.map((arg) => this.evalExpr(arg, env));
          // ……但宏体在「定义环境 + 形参帧」中求值，
          // 调用方的局部绑定对宏体完全不可见。
          const callEnv = new Env(defEnv);
          for (let i = 0; i < def.params.length; i += 1) {
            callEnv.define(def.params[i] as string, {
              tag: 'value',
              value: args[i] as DataValue,
            });
          }
          this.execNodes(def.body, callEnv);
        } finally {
          this.callDepth -= 1;
        }
        return;
      }
    }
  }
}

function mergeBudgets(partial?: Partial<Budgets>): Budgets {
  return { ...DEFAULT_BUDGETS, ...partial };
}

/**
 * 渲染模板。不可信数据先被净化为无原型只读记录，其顶层字段
 * 成为全局变量；宏绑定优先级高于同名字段。任何失败都抛出
 * TemplateError，且不会发布任何输出片段。
 */
export function renderTemplate(
  template: Template,
  data: unknown = {},
  budgets?: Partial<Budgets>,
): string {
  const merged = mergeBudgets(budgets);
  const clean = sanitizeData(data, merged);
  if (!isDataRecord(clean)) {
    throw new TemplateError(
      'INVALID_DATA',
      'top-level template data must be a record',
    );
  }
  const globalEnv = new Env(null);
  for (const key of Object.keys(clean)) {
    globalEnv.define(key, { tag: 'value', value: clean[key] as DataValue });
  }
  return new Interpreter(merged).run(template, globalEnv);
}

export type RenderResult =
  | { readonly ok: true; readonly output: string }
  | { readonly ok: false; readonly error: TemplateError };

/** renderTemplate 的结果式封装：失败时只携带错误，绝无部分输出。 */
export function renderSafe(
  template: Template,
  data?: unknown,
  budgets?: Partial<Budgets>,
): RenderResult {
  try {
    return { ok: true, output: renderTemplate(template, data, budgets) };
  } catch (error) {
    if (error instanceof TemplateError) {
      return { ok: false, error };
    }
    throw error;
  }
}
