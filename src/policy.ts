/** 策略引擎：只读显式属性；条件仅限有限集合成员、整数比较与布尔运算；
 * 不接入任何外部身份服务，求值是纯同步函数。 */

export class PolicyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PolicyError";
  }
}

export type AttrValue = string | number | boolean;

export type Cond =
  | { op: "in"; attr: string; set: readonly AttrValue[] }
  | { op: "eq" | "ne" | "lt" | "le" | "gt" | "ge"; attr: string; value: number }
  | { op: "and" | "or"; args: readonly Cond[] }
  | { op: "not"; arg: Cond };

const CMP_OPS = new Set(["eq", "ne", "lt", "le", "gt", "ge"]);

function isValidSetMember(v: unknown): boolean {
  return (
    typeof v === "string" ||
    typeof v === "boolean" ||
    (typeof v === "number" && Number.isInteger(v))
  );
}

export function validateCond(cond: unknown, path = "$"): asserts cond is Cond {
  if (typeof cond !== "object" || cond === null) {
    throw new PolicyError(`${path}: 条件必须是对象`);
  }
  const c = cond as Record<string, unknown>;
  const op = c.op;
  if (op === "in") {
    if (typeof c.attr !== "string" || c.attr.length === 0) {
      throw new PolicyError(`${path}.attr: 必须是属性名`);
    }
    if (!Array.isArray(c.set)) {
      throw new PolicyError(`${path}.set: 必须是有限集合（数组）`);
    }
    c.set.forEach((v, i) => {
      if (!isValidSetMember(v)) {
        throw new PolicyError(`${path}.set[${i}]: 集合成员只能是字符串、布尔或整数`);
      }
    });
    return;
  }
  if (typeof op === "string" && CMP_OPS.has(op)) {
    if (typeof c.attr !== "string" || c.attr.length === 0) {
      throw new PolicyError(`${path}.attr: 必须是属性名`);
    }
    if (typeof c.value !== "number" || !Number.isInteger(c.value)) {
      throw new PolicyError(`${path}.value: 整数条件仅支持整数，收到 ${String(c.value)}`);
    }
    return;
  }
  if (op === "and" || op === "or") {
    if (!Array.isArray(c.args) || c.args.length === 0) {
      throw new PolicyError(`${path}.args: 布尔运算至少一个子条件`);
    }
    c.args.forEach((a, i) => validateCond(a, `${path}.args[${i}]`));
    return;
  }
  if (op === "not") {
    validateCond(c.arg, `${path}.arg`);
    return;
  }
  throw new PolicyError(
    `${path}.op: 不支持的运算 ${JSON.stringify(op)}；仅支持有限集合 in、整数比较 ${[...CMP_OPS].join("/")} 与布尔运算 and/or/not`,
  );
}

/** 只读显式属性视图：只认声明过的键，读未声明键或写入都会报错。 */
export type Attrs = Readonly<Record<string, AttrValue>>;

export function makeAttrs(declared: Record<string, AttrValue>): Attrs {
  for (const [k, v] of Object.entries(declared)) {
    if (!isValidSetMember(v)) {
      throw new PolicyError(`属性 ${k} 的值非法: 仅允许字符串、布尔或整数`);
    }
  }
  const frozen = Object.freeze({ ...declared });
  return new Proxy(frozen, {
    get(target, prop, receiver) {
      if (typeof prop === "string" && !(prop in target)) {
        throw new PolicyError(
          `未声明的属性: ${prop}（策略只读显式属性，不接外部身份服务）`,
        );
      }
      return Reflect.get(target, prop, receiver);
    },
    set() {
      throw new PolicyError("属性只读，禁止写入");
    },
    defineProperty() {
      throw new PolicyError("属性只读，禁止写入");
    },
  });
}

function readAttr(attrs: Attrs, name: string): AttrValue {
  return (attrs as Record<string, AttrValue>)[name];
}

/** 纯同步求值：仅使用调用方显式提供的属性。 */
export function evaluateCond(cond: Cond, attrs: Attrs): boolean {
  validateCond(cond);
  switch (cond.op) {
    case "in":
      return cond.set.includes(readAttr(attrs, cond.attr));
    case "and":
      return cond.args.every((a) => evaluateCond(a, attrs));
    case "or":
      return cond.args.some((a) => evaluateCond(a, attrs));
    case "not":
      return !evaluateCond(cond.arg, attrs);
    default: {
      const actual = readAttr(attrs, cond.attr);
      if (typeof actual !== "number" || !Number.isInteger(actual)) {
        throw new PolicyError(`属性 ${cond.attr} 不是整数，无法参与整数条件`);
      }
      switch (cond.op) {
        case "eq": return actual === cond.value;
        case "ne": return actual !== cond.value;
        case "lt": return actual < cond.value;
        case "le": return actual <= cond.value;
        case "gt": return actual > cond.value;
        case "ge": return actual >= cond.value;
      }
    }
  }
}
