/**
 * 动态响应式表单验证与依赖交叉校验器。
 *
 * 核心能力:
 * 1. 字段间依赖图:字段通过 `dependsOn` 声明所依赖的其他字段,
 *    被依赖字段的值变化时,所有依赖方会被自动重新校验(错误消息即时刷新)。
 * 2. 动态规则:`dynamicRules(values)` 根据整表当前值实时计算规则集合,
 *    例如“勾选已婚后,配偶姓名变为必填”。
 * 3. 整表一键校验:`validate()` 返回整表合法性并刷新所有字段状态。
 */

import type { Rule, ValidationContext } from './rules.ts';

export interface FieldSchema {
  /** 静态规则:始终生效 */
  rules?: Rule[];
  /** 动态规则:每次校验时根据整表当前值计算,常用于条件必填 */
  dynamicRules?: (values: Record<string, unknown>) => Rule[];
  /** 本字段依赖的其他字段;这些字段值变化时,本字段会被自动重新校验 */
  dependsOn?: string[];
}

export interface FieldState {
  readonly value: unknown;
  /** 当前错误消息列表,空数组表示合法 */
  readonly errors: readonly string[];
  readonly valid: boolean;
  readonly invalid: boolean;
  /** 值是否被 setValue 修改过 */
  readonly dirty: boolean;
  /** 是否至少经历过一次校验 */
  readonly validated: boolean;
}

export interface UpdateEvent {
  /** 触发本次变化的字段(整表校验时为 null) */
  readonly source: string | null;
  /** 本次被重新校验的字段(按校验顺序) */
  readonly revalidated: readonly string[];
}

export type UpdateListener = (event: UpdateEvent) => void;

interface MutableFieldState {
  value: unknown;
  errors: string[];
  valid: boolean;
  invalid: boolean;
  dirty: boolean;
  validated: boolean;
}

export class FormValidator {
  readonly #schemas: Readonly<Record<string, FieldSchema>>;
  readonly #values: Record<string, unknown> = {};
  readonly #states = new Map<string, MutableFieldState>();
  /** 被依赖字段 -> 依赖它的字段集合 */
  readonly #dependents = new Map<string, Set<string>>();
  readonly #listeners = new Set<UpdateListener>();

  constructor(
    schemas: Record<string, FieldSchema>,
    initialValues: Record<string, unknown> = {},
  ) {
    this.#schemas = schemas;
    for (const name of Object.keys(schemas)) {
      this.#values[name] = initialValues[name];
      this.#states.set(name, {
        value: initialValues[name],
        errors: [],
        valid: false,
        invalid: false,
        dirty: false,
        validated: false,
      });
    }
    // 构建依赖图(反向索引)
    for (const [name, schema] of Object.entries(schemas)) {
      for (const dep of schema.dependsOn ?? []) {
        if (!(dep in schemas)) {
          throw new Error(`字段 "${name}" 依赖了未注册的字段 "${dep}"`);
        }
        let set = this.#dependents.get(dep);
        if (!set) {
          set = new Set();
          this.#dependents.set(dep, set);
        }
        set.add(name);
      }
    }
  }

  /** 订阅任意字段状态变化,返回取消订阅函数 */
  onUpdate(listener: UpdateListener): () => void {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  getValue(name: string): unknown {
    this.#assertField(name);
    return this.#values[name];
  }

  getValues(): Record<string, unknown> {
    return { ...this.#values };
  }

  getState(name: string): FieldState {
    this.#assertField(name);
    const state = this.#states.get(name)!;
    return { ...state, errors: [...state.errors] };
  }

  getErrors(name: string): string[] {
    return [...this.#states.get(this.#assertField(name))!.errors];
  }

  /** 整表错误:字段名 -> 错误消息列表(仅包含有错误的字段) */
  getAllErrors(): Record<string, string[]> {
    const result: Record<string, string[]> = {};
    for (const [name, state] of this.#states) {
      if (state.errors.length > 0) result[name] = [...state.errors];
    }
    return result;
  }

  /** 所有字段均已校验且合法 */
  isValid(): boolean {
    for (const state of this.#states.values()) {
      if (!state.validated || state.invalid) return false;
    }
    return true;
  }

  /** 写入字段值,并立即重新校验该字段及其全部依赖方 */
  setValue(name: string, value: unknown): void {
    this.#assertField(name);
    this.#values[name] = value;
    const state = this.#states.get(name)!;
    state.value = value;
    state.dirty = true;

    const affected = [name, ...this.#dependentsOf(name)];
    for (const field of affected) this.#validateField(field);
    this.#emit({ source: name, revalidated: affected });
  }

  /** 手动触发单个字段(及其依赖方)重新校验 */
  validateField(name: string): boolean {
    this.#assertField(name);
    const affected = [name, ...this.#dependentsOf(name)];
    let allValid = true;
    for (const field of affected) {
      if (!this.#validateField(field)) allValid = false;
    }
    this.#emit({ source: name, revalidated: affected });
    return allValid;
  }

  /** 整表一键校验:刷新所有字段状态,返回整表是否合法 */
  validate(): boolean {
    let allValid = true;
    for (const name of Object.keys(this.#schemas)) {
      if (!this.#validateField(name)) allValid = false;
    }
    this.#emit({ source: null, revalidated: Object.keys(this.#schemas) });
    return allValid;
  }

  /** 收集某字段当前生效的全部规则(静态 + 动态) */
  activeRules(name: string): Rule[] {
    this.#assertField(name);
    const schema = this.#schemas[name];
    return [
      ...(schema.rules ?? []),
      ...(schema.dynamicRules?.(this.#values) ?? []),
    ];
  }

  #validateField(name: string): boolean {
    const state = this.#states.get(name)!;
    const ctx: ValidationContext = { values: this.#values, field: name };
    const errors: string[] = [];
    for (const rule of this.activeRules(name)) {
      const result = rule.validate(state.value, ctx);
      if (result !== true) {
        errors.push(typeof result === 'string' ? result : rule.message);
      }
    }
    state.errors = errors;
    state.valid = errors.length === 0;
    state.invalid = !state.valid;
    state.validated = true;
    return state.valid;
  }

  /** 依赖 name 的字段集合(直接依赖方) */
  #dependentsOf(name: string): string[] {
    return [...(this.#dependents.get(name) ?? [])];
  }

  #assertField(name: string): string {
    if (!(name in this.#schemas)) {
      throw new Error(`未注册的字段: "${name}"`);
    }
    return name;
  }

  #emit(event: UpdateEvent): void {
    for (const listener of this.#listeners) listener(event);
  }
}
