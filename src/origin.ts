/**
 * 来源（origin）模型。
 *
 * 两类来源：
 *  - 具名来源：由来源字符串标识（如 "https://camera.example"），相同字符串即同一来源；
 *  - 不透明来源：由唯一令牌标识，两个不透明来源只有在令牌完全相同时才视为同一来源。
 *
 * 不透明来源对应浏览器中的 opaque origin：每次生成默认拿到一个全新的、永不复用的令牌。
 */

export interface NamedOrigin {
  readonly kind: "origin";
  readonly value: string;
}

export interface OpaqueOrigin {
  readonly kind: "opaque";
  readonly token: string;
}

export type Origin = NamedOrigin | OpaqueOrigin;

let opaqueCounter = 0;

function randomSuffix(): string {
  const cryptoApi = globalThis.crypto as { randomUUID?: () => string } | undefined;
  if (cryptoApi && typeof cryptoApi.randomUUID === "function") {
    return cryptoApi.randomUUID();
  }
  opaqueCounter += 1;
  return `rand-${opaqueCounter}-${Math.random().toString(36).slice(2)}`;
}

/** 构造一个具名来源。相同 value 的两次构造在匹配时等价。 */
export function origin(value: string): NamedOrigin {
  return { kind: "origin", value };
}

/**
 * 构造一个不透明来源。
 * 不传 token 时生成进程内唯一的新令牌；传入 token 时用于显式引用某个既定不透明来源。
 */
export function opaqueOrigin(token?: string): OpaqueOrigin {
  opaqueCounter += 1;
  const resolvedToken = token ?? `opaque-${opaqueCounter}-${randomSuffix()}`;
  return { kind: "opaque", token: resolvedToken };
}

/** 来源的规范化键，用于集合去重与比较。 */
export function originKey(value: Origin): string {
  return value.kind === "opaque" ? `opaque:${value.token}` : `origin:${value.value}`;
}

/**
 * 判断两个来源是否同一来源。
 * 具名来源按字符串比较；不透明来源仅在令牌完全相同时相等，且永不与具名来源相等。
 */
export function sameOrigin(left: Origin, right: Origin): boolean {
  return originKey(left) === originKey(right);
}
