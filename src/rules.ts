export type Values = Record<string, unknown>;

export interface ValidationContext {
  values: Values;
  field: string;
}

/**
 * A validation rule. Returns an error message when invalid, or null when valid.
 * `dependsOn` lists OTHER fields whose values this rule reads (directly or via
 * `when`); the form engine uses it to build the reactive dependency graph so
 * that changing any of those fields re-triggers this rule.
 */
export interface Rule {
  readonly name: string;
  /** Other fields this rule reads. Used for reactive re-validation. */
  readonly dependsOn?: readonly string[];
  /** Dynamic activation: rule only applies while this returns true. */
  readonly when?: (values: Values) => boolean;
  validate(value: unknown, ctx: ValidationContext): string | null;
}

const isEmpty = (value: unknown): boolean =>
  value === undefined ||
  value === null ||
  (typeof value === "string" && value.trim() === "") ||
  (Array.isArray(value) && value.length === 0);

export function required(message = "该字段为必填项"): Rule {
  return {
    name: "required",
    validate: (value) => (isEmpty(value) ? message : null),
  };
}

/**
 * Conditionally-required rule: becomes a required rule only while `predicate`
 * holds. `dependsOn` must list every field read by the predicate so the engine
 * can re-evaluate it reactively (e.g. a checkbox toggling a required field).
 */
export function requiredWhen(
  predicate: (values: Values) => boolean,
  dependsOn: readonly string[],
  message = "该字段为必填项",
): Rule {
  return {
    name: "requiredWhen",
    dependsOn,
    when: predicate,
    validate: (value) => (isEmpty(value) ? message : null),
  };
}

export function minLength(min: number, message?: string): Rule {
  return {
    name: "minLength",
    validate: (value) => {
      if (isEmpty(value)) return null; // emptiness is `required`'s job
      const len = String(value).length;
      return len < min ? (message ?? `长度不能少于 ${min} 个字符`) : null;
    },
  };
}

export function maxLength(max: number, message?: string): Rule {
  return {
    name: "maxLength",
    validate: (value) => {
      if (isEmpty(value)) return null;
      const len = String(value).length;
      return len > max ? (message ?? `长度不能超过 ${max} 个字符`) : null;
    },
  };
}

export function pattern(regex: RegExp, message = "格式不正确"): Rule {
  return {
    name: "pattern",
    validate: (value) => {
      if (isEmpty(value)) return null;
      return regex.test(String(value)) ? null : message;
    },
  };
}

export function email(message = "邮箱格式不正确"): Rule {
  return pattern(/^[^\s@]+@[^\s@]+\.[^\s@]+$/, message);
}

/**
 * Cross-field rule: this field must equal another field's current value
 * (e.g. confirm-password vs password). Declares a dependency so editing the
 * target field instantly re-validates this one.
 */
export function matchesField(otherField: string, message?: string): Rule {
  return {
    name: "matchesField",
    dependsOn: [otherField],
    validate: (value, ctx) => {
      if (isEmpty(value)) return null;
      return value === ctx.values[otherField]
        ? null
        : (message ?? `必须与「${otherField}」保持一致`);
    },
  };
}

/** Escape hatch for arbitrary custom / cross-field logic. */
export function custom(
  validate: (value: unknown, ctx: ValidationContext) => string | null,
  options: {
    name?: string;
    dependsOn?: readonly string[];
    when?: (values: Values) => boolean;
  } = {},
): Rule {
  return {
    name: options.name ?? "custom",
    dependsOn: options.dependsOn,
    when: options.when,
    validate,
  };
}
