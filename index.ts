/**
 * 异常快照：把任意异常对象（含嵌套 cause 图）转换为可安全日志输出的纯数据快照。
 *
 * 安全保证：
 * - 只读取自有「数据属性」（通过 getOwnPropertyDescriptor 取 descriptor.value），
 *   绝不执行 getter、toJSON 或任何用户代码。
 * - 输入对象不被修改；输出只含新分配的纯数据，可直接 JSON.stringify。
 *
 * 结构保证：
 * - 共享 / 循环引用：每个对象节点分配唯一 $id，再次出现时输出 { $ref: id }。
 * - 用户属性全部放在 props/items 内，$ 前缀键只出现在包装层，引用编号无歧义。
 *
 * 预算保证：maxNodes / maxDepth / maxProperties / maxStringLength 共同约束
 * 总节点数与总文本量，超出部分以占位标记截断。
 */

export type FieldPolicy = "remove" | "replace";

export interface SnapshotOptions {
  /** 对象/数组节点总数上限（含根），超出后输出截断标记。默认 200。 */
  maxNodes?: number;
  /** 递归深度上限。默认 16。 */
  maxDepth?: number;
  /** 单个对象的属性数 / 数组元素数上限。默认 50。 */
  maxProperties?: number;
  /** 单个字符串长度上限，超出截断。默认 1024。 */
  maxStringLength?: number;
  /** 命中敏感字段且策略为 replace 时的替换文本。默认 "[REDACTED]"。 */
  replacement?: string;
  /** 命中内置/自定义敏感字段名时的默认策略。默认 "replace"。 */
  defaultPolicy?: FieldPolicy;
  /** 追加的敏感字段名（大小写不敏感），使用 defaultPolicy。 */
  redactKeys?: readonly string[];
  /** 逐字段策略，键大小写不敏感，优先级高于 redactKeys/defaultPolicy。 */
  fieldPolicy?: Record<string, FieldPolicy>;
}

export interface SnapshotResult {
  /** 纯数据快照，可安全 JSON.stringify。 */
  snapshot: unknown;
  /** 实际访问的对象节点数。 */
  nodeCount: number;
  /** 是否因任一预算耗尽而发生截断。 */
  truncated: boolean;
}

/** 内置敏感字段名（小写）。 */
export const DEFAULT_SENSITIVE_KEYS: readonly string[] = [
  "token",
  "access_token",
  "refresh_token",
  "id_token",
  "authorization",
  "proxy-authorization",
  "cookie",
  "set-cookie",
  "password",
  "passwd",
  "secret",
  "client_secret",
  "api_key",
  "apikey",
  "api-key",
  "x-api-key",
  "session",
  "sessionid",
  "private_key",
  "privatekey",
  "credential",
  "credentials",
  "csrf",
  "xsrf",
];

const ACCESSOR_OMITTED = "[omitted: accessor]";
const UNREADABLE = "[omitted: unreadable property]";

/**
 * V8 把 Error 实例的 stack 实现为自有访问器。仅在访问器的 get 与引擎内置
 * stack getter 严格同一函数时才调用它——用户自定义 getter 不可能通过该检查。
 */
const BUILTIN_STACK_GETTER: (() => unknown) | undefined = (() => {
  try {
    const desc = Object.getOwnPropertyDescriptor(new Error(), "stack");
    if (desc && typeof desc.get === "function") return desc.get as () => unknown;
  } catch {
    // 忽略
  }
  return undefined;
})();

interface ResolvedOptions {
  maxNodes: number;
  maxDepth: number;
  maxProperties: number;
  maxStringLength: number;
  replacement: string;
  defaultPolicy: FieldPolicy;
  sensitive: Set<string>;
  fieldPolicy: Map<string, FieldPolicy>;
}

function resolveOptions(options: SnapshotOptions): ResolvedOptions {
  const sensitive = new Set(DEFAULT_SENSITIVE_KEYS);
  for (const key of options.redactKeys ?? []) sensitive.add(key.toLowerCase());
  const fieldPolicy = new Map<string, FieldPolicy>();
  for (const [key, policy] of Object.entries(options.fieldPolicy ?? {})) {
    fieldPolicy.set(key.toLowerCase(), policy);
  }
  return {
    maxNodes: options.maxNodes ?? 200,
    maxDepth: options.maxDepth ?? 16,
    maxProperties: options.maxProperties ?? 50,
    maxStringLength: options.maxStringLength ?? 1024,
    replacement: options.replacement ?? "[REDACTED]",
    defaultPolicy: options.defaultPolicy ?? "replace",
    sensitive,
    fieldPolicy,
  };
}

/** 沿原型链只读数据描述符，取构造函数名；不触发任何用户 getter。 */
function safeTypeName(obj: object): string {
  let proto: object | null;
  try {
    proto = Object.getPrototypeOf(obj);
  } catch {
    return "Object";
  }
  while (proto !== null) {
    let ctor: unknown;
    try {
      const desc = Object.getOwnPropertyDescriptor(proto, "constructor");
      ctor = desc && "value" in desc ? desc.value : undefined;
    } catch {
      return "Object";
    }
    if (typeof ctor === "function") {
      try {
        const nameDesc = Object.getOwnPropertyDescriptor(ctor, "name");
        if (nameDesc && "value" in nameDesc && typeof nameDesc.value === "string" && nameDesc.value) {
          return nameDesc.value;
        }
      } catch {
        // 继续向上找
      }
    }
    try {
      proto = Object.getPrototypeOf(proto);
    } catch {
      return "Object";
    }
  }
  return "Object";
}

function safeFunctionName(fn: Function): string {
  try {
    const desc = Object.getOwnPropertyDescriptor(fn, "name");
    if (desc && "value" in desc && typeof desc.value === "string") return desc.value;
  } catch {
    // 忽略
  }
  return "";
}

class Snapshotter {
  private readonly seen = new Map<object, number>();
  private nodes = 0;
  truncated = false;
  private readonly opts: ResolvedOptions;

  constructor(opts: ResolvedOptions) {
    this.opts = opts;
  }

  get nodeCount(): number {
    return this.nodes;
  }

  visit(value: unknown, depth: number): unknown {
    if (value === null) return null;
    switch (typeof value) {
      case "undefined":
        return "[undefined]";
      case "boolean":
        return value;
      case "number":
        return Number.isFinite(value) ? value : String(value);
      case "string":
        return this.truncateString(value);
      case "bigint":
        return `${value}n`;
      case "symbol":
        return String(value);
      case "function":
        return `[function ${safeFunctionName(value as Function)}]`;
      default:
        return this.visitObject(value as object, depth);
    }
  }

  private truncateString(text: string): string {
    const max = this.opts.maxStringLength;
    if (text.length <= max) return text;
    this.truncated = true;
    return `${text.slice(0, max)}…[truncated ${text.length - max} chars]`;
  }

  private visitObject(obj: object, depth: number): unknown {
    const existing = this.seen.get(obj);
    if (existing !== undefined) return { $ref: existing };

    if (this.nodes >= this.opts.maxNodes) {
      this.truncated = true;
      return "[truncated: node budget exhausted]";
    }
    if (depth > this.opts.maxDepth) {
      this.truncated = true;
      return "[truncated: max depth exceeded]";
    }
    const id = ++this.nodes;
    this.seen.set(obj, id);

    // Date / RegExp 用原型方法直接调用，不经过实例上可能被覆盖的方法。
    try {
      if (obj instanceof Date) {
        const time = Date.prototype.getTime.call(obj);
        return { $id: id, $type: "Date", value: Number.isNaN(time) ? "Invalid Date" : new Date(time).toISOString() };
      }
      if (obj instanceof RegExp) {
        return { $id: id, $type: "RegExp", value: RegExp.prototype.toString.call(obj) };
      }
    } catch {
      // 落到通用对象处理
    }

    let names: string[];
    try {
      names = Object.getOwnPropertyNames(obj);
    } catch {
      this.truncated = true;
      return { $id: id, $type: safeTypeName(obj), $note: UNREADABLE };
    }

    const isArray = Array.isArray(obj);
    const props: Record<string, unknown> = Object.create(null);
    const items: unknown[] = [];
    let omittedProps = 0;
    let kept = 0;

    for (const key of names) {
      if (isArray && /^(0|[1-9]\d*)$/.test(key)) {
        // 数组索引单独走 items，仍受同一属性预算约束。
        if (kept >= this.opts.maxProperties) {
          omittedProps++;
          continue;
        }
        kept++;
        items[Number(key)] = this.readOwn(obj, key, depth);
        continue;
      }
      if (key === "length" && isArray) continue;

      const policy = this.policyFor(key);
      if (policy === "remove") continue;
      if (kept >= this.opts.maxProperties) {
        omittedProps++;
        continue;
      }
      kept++;
      if (policy === "replace") {
        props[key] = this.opts.replacement;
        continue;
      }
      props[key] = this.readOwn(obj, key, depth);
    }

    const node: Record<string, unknown> = Object.create(null);
    node.$id = id;
    node.$type = safeTypeName(obj);
    if (isArray) node.items = items;
    if (Object.keys(props).length > 0) node.props = props;
    if (omittedProps > 0) {
      this.truncated = true;
      node.$truncated = `${omittedProps} propert${omittedProps === 1 ? "y" : "ies"} omitted`;
    }
    return node;
  }

  /** 只取自有数据属性的值；访问器属性跳过，绝不调用。 */
  private readOwn(obj: object, key: string, depth: number): unknown {
    let desc: PropertyDescriptor | undefined;
    try {
      desc = Object.getOwnPropertyDescriptor(obj, key);
    } catch {
      return UNREADABLE;
    }
    if (!desc) return "[undefined]";
    if (!("value" in desc)) {
      if (
        key === "stack" &&
        obj instanceof Error &&
        BUILTIN_STACK_GETTER !== undefined &&
        desc.get === BUILTIN_STACK_GETTER
      ) {
        try {
          return this.visit(BUILTIN_STACK_GETTER.call(obj), depth + 1);
        } catch {
          return UNREADABLE;
        }
      }
      return ACCESSOR_OMITTED;
    }
    return this.visit(desc.value, depth + 1);
  }

  private policyFor(key: string): FieldPolicy | undefined {
    const lower = key.toLowerCase();
    const explicit = this.opts.fieldPolicy.get(lower);
    if (explicit) return explicit;
    if (this.opts.sensitive.has(lower)) return this.opts.defaultPolicy;
    return undefined;
  }
}

/** 生成异常的脱敏快照（纯数据）。 */
export function snapshotError(error: unknown, options: SnapshotOptions = {}): SnapshotResult {
  const snapshotter = new Snapshotter(resolveOptions(options));
  const snapshot = snapshotter.visit(error, 0);
  return { snapshot, nodeCount: snapshotter.nodeCount, truncated: snapshotter.truncated };
}

/** 生成快照并序列化为 JSON 字符串。序列化只作用于快照，不接触原对象。 */
export function snapshotToJson(error: unknown, options: SnapshotOptions = {}): string {
  return JSON.stringify(snapshotError(error, options).snapshot);
}
