/**
 * 可组合的 Trusted Types 策略库。
 *
 * 安全模型：
 * - TrustedHTML 只能由 Policy 创建（构造器需要模块内密钥）。
 * - 每个 Policy 有唯一身份（对象本身即品牌），名称仅用于注册表查找。
 * - 字符串化（toString/String/模板拼接）得到的是普通 string，
 *   没有任何 API 可以把 string 重新铸造成 TrustedHTML。
 * - sink 通过 WeakMap 溯源，只接受许可策略创建的值；伪造对象无法通过。
 */

const INTERNAL = Symbol('trusted.internal');

export class TrustedHTML {
  readonly #value: string;

  constructor(secret: symbol, value: string) {
    if (secret !== INTERNAL) {
      throw new TypeError('TrustedHTML 只能由 Policy 创建，不能直接构造');
    }
    this.#value = value;
    Object.freeze(this);
  }

  /** 字符串化即失去品牌：返回值是普通 string，无法重新获得信任。 */
  toString(): string {
    return this.#value;
  }
}

/** TrustedHTML 实例 -> 创建它的 Policy（模块内私有，外部无法伪造）。 */
const provenance = new WeakMap<TrustedHTML, Policy>();

export interface ComposedInput {
  /** true 表示该输入来自许可的上游策略，false 表示不可信字符串。 */
  readonly trusted: boolean;
  readonly value: string;
}

export class Policy {
  readonly name: string;
  readonly #transform: (parts: readonly ComposedInput[]) => string;
  readonly #upstream: ReadonlySet<Policy>;
  readonly isDefault: boolean;

  constructor(
    secret: symbol,
    name: string,
    transform: (parts: readonly ComposedInput[]) => string,
    upstream: readonly Policy[],
    isDefault = false,
  ) {
    if (secret !== INTERNAL) {
      throw new TypeError('Policy 只能由 PolicyRegistry 创建');
    }
    this.name = name;
    this.#transform = transform;
    this.#upstream = new Set(upstream);
    this.isDefault = isDefault;
  }

  /**
   * 创建可信 HTML。输入可以是不可信字符串，或来自本策略声明的
   * 上游策略的 TrustedHTML；其他来源的值会被拒绝。
   */
  createHTML(...inputs: Array<string | TrustedHTML>): TrustedHTML {
    const parts: ComposedInput[] = inputs.map((input) => {
      if (input instanceof TrustedHTML) {
        const source = provenance.get(input);
        if (!source || !this.#upstream.has(source)) {
          throw new TypeError(
            `策略 "${this.name}" 未声明信任来自策略 "${source?.name ?? 'unknown'}" 的值`,
          );
        }
        return { trusted: true, value: input.toString() };
      }
      if (typeof input === 'string') {
        return { trusted: false, value: input };
      }
      throw new TypeError('createHTML 只接受 string 或 TrustedHTML 输入');
    });
    const output = this.#transform(parts);
    if (typeof output !== 'string') {
      throw new TypeError(`策略 "${this.name}" 的转换函数必须返回 string`);
    }
    const result = new TrustedHTML(INTERNAL, output);
    provenance.set(result, this);
    return result;
  }
}

export const DEFAULT_POLICY_NAME = 'default';

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

export interface ComposedPolicyOptions {
  /** 允许解包的上游策略；不在列表中的策略值会被拒绝。 */
  readonly from: readonly Policy[];
  readonly createHTML: (parts: readonly ComposedInput[]) => string;
}

export class PolicyRegistry {
  readonly #policies = new Map<string, Policy>();
  readonly #defaultPolicy: Policy;

  constructor() {
    this.#defaultPolicy = new Policy(
      INTERNAL,
      DEFAULT_POLICY_NAME,
      (parts) => parts.map((p) => escapeHtml(p.value)).join(''),
      [],
      true,
    );
  }

  /** default 策略只做转义，输出品牌是 default，不会获得其他策略的许可。 */
  get defaultPolicy(): Policy {
    return this.#defaultPolicy;
  }

  createPolicy(name: string, transform: (input: string) => string): Policy {
    this.#assertNameAvailable(name);
    const policy = new Policy(INTERNAL, name, (parts) => {
      if (parts.length !== 1) {
        throw new TypeError(`策略 "${name}" 只接受单个输入`);
      }
      return transform(parts[0].value);
    }, []);
    this.#policies.set(name, policy);
    return policy;
  }

  /** 合成策略：可解包声明的上游策略值，输出带有自己的全新品牌。 */
  createComposedPolicy(name: string, options: ComposedPolicyOptions): Policy {
    this.#assertNameAvailable(name);
    const policy = new Policy(INTERNAL, name, options.createHTML, options.from);
    this.#policies.set(name, policy);
    return policy;
  }

  /**
   * 按名查找；未命中时回退到 default 策略。
   * 回退值的来源品牌仍是 default，不会借用到请求名字的许可。
   */
  getPolicy(name: string): Policy {
    return this.#policies.get(name) ?? this.#defaultPolicy;
  }

  #assertNameAvailable(name: string): void {
    if (typeof name !== 'string' || name.length === 0) {
      throw new TypeError('策略名必须是非空字符串');
    }
    if (name === DEFAULT_POLICY_NAME) {
      throw new Error(`"${DEFAULT_POLICY_NAME}" 是保留策略名`);
    }
    if (this.#policies.has(name)) {
      throw new Error(`策略名重复: "${name}"`);
    }
  }
}

export interface HtmlTarget {
  innerHTML: string;
}

export class TrustedHtmlSink {
  readonly #target: HtmlTarget;
  readonly #allowed: ReadonlySet<Policy>;

  constructor(target: HtmlTarget, allowed: readonly Policy[]) {
    if (allowed.length === 0) {
      throw new TypeError('sink 至少需要一个许可策略');
    }
    this.#target = target;
    this.#allowed = new Set(allowed);
  }

  /** 只有许可策略创建的 TrustedHTML 才能写入；其余一律拒绝且不触碰目标。 */
  setHTML(value: unknown): void {
    if (!(value instanceof TrustedHTML)) {
      throw new TypeError('sink 只接受 TrustedHTML，普通字符串/对象不受信任');
    }
    const source = provenance.get(value);
    if (!source || !this.#allowed.has(source)) {
      throw new TypeError(
        `策略 "${source?.name ?? 'unknown'}" 不在此 sink 的许可列表中`,
      );
    }
    this.#target.innerHTML = value.toString();
  }
}

export function createHtmlSink(
  target: HtmlTarget,
  allowed: readonly Policy[],
): TrustedHtmlSink {
  return new TrustedHtmlSink(target, allowed);
}
