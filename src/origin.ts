/**
 * 结构化来源（origin）模型。
 *
 * 设计要点：
 * - 普通来源是不可变元组（scheme / host / port），授权比较始终逐字段进行。
 * - 不透明来源携带一个进程内唯一、外部无法伪造的随机身份；两个独立生成的
 *   不透明来源永远不同源，即使它们的显示序列化结果都是 "null"。
 * - 序列化（toString）只服务于日志/调试等显示场景，任何授权路径都不得依赖
 *   字符串形式：字符串 "null" 无法区分具体的不透明来源。
 */

import { randomUUID } from "node:crypto";

/** 来源类别标签，便于判别联合类型与显式检查。 */
export type OriginKind = "tuple" | "opaque";

/**
 * 普通（元组）来源。
 *
 * scheme 与 host 均已规范化为小写；port 为有效 TCP/UDP 端口号（1-65535），
 * 当输入未显式给定时解析为该 scheme 的默认端口。
 */
export interface TupleOrigin {
  readonly kind: "tuple";
  readonly scheme: string;
  readonly host: string;
  readonly port: number;
}

/**
 * 不透明来源。
 *
 * 对象本身即身份令牌：真实身份保存在进程内的 {@link opaqueIdentities}
 * WeakMap 中，不出现在可枚举字段里，因而无法被复制、序列化或根据显示
 * 字符串重建。这同时兼容 HTML 结构化克隆算法（该算法会丢弃 symbol 值）：
 * {@link structuredClone} 产生的是一个没有身份登记的新对象，天然是另一个
 * 不透明来源；同一沙箱内的身份继承必须显式走 {@link cloneOrigin}。
 */
export interface OpaqueOrigin {
  readonly kind: "opaque";
  /** 仅用于日志显示的随机 ID，不参与任何授权判断。 */
  readonly identityId: string;
}

export type Origin = TupleOrigin | OpaqueOrigin;

/** 各 scheme 的默认端口；与 WHATWG URL 规范的特殊 scheme 表保持一致。 */
const DEFAULT_PORTS: Readonly<Record<string, number>> = Object.freeze({
  http: 80,
  https: 443,
  ws: 80,
  wss: 443,
  ftp: 21,
});

const MAX_PORT = 65535;

/**
 * 不透明身份登记表：键为不透明来源对象，值为其随机身份 ID。
 * 进程内私有，外部无法枚举或伪造条目。
 */
const opaqueIdentities = new WeakMap<OpaqueOrigin, string>();

/** 取 scheme 的默认端口；未知 scheme 没有默认端口。 */
export function defaultPort(scheme: string): number | undefined {
  return DEFAULT_PORTS[scheme.toLowerCase()];
}

function normalizeScheme(scheme: string): string {
  if (typeof scheme !== "string" || scheme.length === 0) {
    throw new TypeError("scheme must be a non-empty string");
  }
  const normalized = scheme.toLowerCase();
  if (!/^[a-z][a-z0-9+.-]*$/.test(normalized)) {
    throw new TypeError(`invalid scheme: ${scheme}`);
  }
  return normalized;
}

/**
 * 校验 ASCII 主机。
 *
 * 接受：
 * - 方括号包裹的 IPv6 字面量（如 [::1]、[2001:db8::1]）
 * - IPv4 字面量（如 127.0.0.1）
 * - 常规 ASCII 域名（label 由字母数字与连字符构成，点分，允许末尾根点）
 * 明确拒绝非 ASCII 字符（punycode/IDNA 转换应在进入本库之前完成）。
 */
function normalizeHost(host: string): string {
  if (typeof host !== "string" || host.length === 0) {
    throw new TypeError("host must be a non-empty string");
  }
  if (/[^\x20-\x7e]/.test(host)) {
    throw new TypeError(`host must be ASCII-only: ${host}`);
  }

  if (host.startsWith("[")) {
    if (!host.endsWith("]")) {
      throw new TypeError(`malformed IPv6 host, missing closing bracket: ${host}`);
    }
    const address = host.slice(1, -1);
    if (address.length === 0 || !/^[0-9a-f:.]+$/i.test(address) || !address.includes(":")) {
      throw new TypeError(`invalid IPv6 host: ${host}`);
    }
    return `[${address.toLowerCase()}]`;
  }

  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(host)) {
    const octets = host.split(".").map((part) => Number(part));
    if (octets.some((octet) => octet > 255)) {
      throw new TypeError(`invalid IPv4 host: ${host}`);
    }
    return host;
  }

  const withoutRootDot = host.endsWith(".") ? host.slice(0, -1) : host;
  const labels = withoutRootDot.split(".");
  const labelPattern = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/i;
  if (labels.length === 0 || labels.some((label) => !labelPattern.test(label))) {
    throw new TypeError(`invalid ASCII host: ${host}`);
  }
  return withoutRootDot.toLowerCase();
}

function normalizePort(port: number | undefined, scheme: string): number {
  if (port === undefined) {
    const fallback = DEFAULT_PORTS[scheme];
    if (fallback === undefined) {
      throw new TypeError(
        `port is required for scheme "${scheme}" (no registered default port)`,
      );
    }
    return fallback;
  }
  if (!Number.isInteger(port) || port < 1 || port > MAX_PORT) {
    throw new TypeError(`port must be an integer in [1, ${MAX_PORT}], got: ${String(port)}`);
  }
  return port;
}

/**
 * 构造普通来源。
 *
 * @param scheme 方案，大小写不敏感，内部规范化为小写
 * @param host ASCII 主机（域名 / IPv4 / [IPv6]），域名规范化为小写
 * @param port 可选端口；省略时使用 scheme 默认端口，未知 scheme 必须显式给出
 */
export function tupleOrigin(scheme: string, host: string, port?: number): TupleOrigin {
  const normalizedScheme = normalizeScheme(scheme);
  const normalizedHost = normalizeHost(host);
  const normalizedPort = normalizePort(port, normalizedScheme);
  return Object.freeze({
    kind: "tuple" as const,
    scheme: normalizedScheme,
    host: normalizedHost,
    port: normalizedPort,
  });
}

/**
 * 构造一个新的不透明来源。
 *
 * 每次调用都产生独立随机身份，因而与任何其他不透明来源（包括此前创建的）
 * 都不同源。沙箱化文档等场景应通过 {@link inheritSandboxOrigin} 继承身份，
 * 而不是重新生成。
 */
export function opaqueOrigin(): OpaqueOrigin {
  const identityId = randomUUID();
  const origin: OpaqueOrigin = Object.freeze({
    kind: "opaque" as const,
    identityId,
  });
  opaqueIdentities.set(origin, identityId);
  return origin;
}

/** 类型守卫：是否为不透明来源。 */
export function isOpaque(origin: Origin): origin is OpaqueOrigin {
  return origin.kind === "opaque";
}

/** 类型守卫：是否为普通（元组）来源。 */
export function isTuple(origin: Origin): origin is TupleOrigin {
  return origin.kind === "tuple";
}

/**
 * 同源判定——授权决策唯一允许使用的比较方式。
 *
 * - 两个普通来源：scheme / host / port 全部相等才同源（结构化比较）。
 * - 两个不透明来源：仅当持有同一个随机身份令牌时同源；分别新建的两个
 *   不透明来源必然返回 false，尽管它们 toString() 都是 "null"。
 * - 跨类别（普通 vs 不透明）永远不同源。
 */
export function sameOrigin(left: Origin, right: Origin): boolean {
  if (left.kind !== right.kind) {
    return false;
  }
  if (left.kind === "opaque" && right.kind === "opaque") {
    if (left === right) {
      return true;
    }
    const leftIdentity = opaqueIdentities.get(left);
    const rightIdentity = opaqueIdentities.get(right);
    if (leftIdentity === undefined || rightIdentity === undefined) {
      return false;
    }
    return leftIdentity === rightIdentity;
  }
  return (
    left.scheme === right.scheme &&
    left.host === right.host &&
    left.port === right.port
  );
}

/**
 * 显示序列化。
 *
 * 不透明来源序列化为 "null"（与浏览器 document.origin 的显示约定一致）。
 * 警告：该字符串只可用于显示，禁止用于授权比较——"null" 无法区分不同的
 * 不透明来源。授权请使用 {@link sameOrigin}。
 */
export function serializeOrigin(origin: Origin): string {
  if (origin.kind === "opaque") {
    return "null";
  }
  const { scheme, host, port } = origin;
  if (DEFAULT_PORTS[scheme] === port) {
    return `${scheme}://${host}`;
  }
  return `${scheme}://${host}:${port}`;
}

/**
 * 仅解析普通来源的序列化字符串。
 *
 * 传入 "null" 会直接抛错：不透明来源不可由字符串重建（这正是随机身份要
 * 保证的性质），从而杜绝把字符串 "null" 当成某个不透明来源接受下来的
 * 反模式。
 */
export function parseTupleOrigin(text: string): TupleOrigin {
  if (typeof text !== "string") {
    throw new TypeError("origin text must be a string");
  }
  const trimmed = text.trim();
  if (trimmed === "null") {
    throw new TypeError(
      'cannot reconstruct an opaque origin from the display string "null"; ' +
        "opaque origins are unforgeable and must be inherited, not parsed",
    );
  }
  const authorityStart = trimmed.indexOf("://");
  if (authorityStart === -1) {
    throw new TypeError(`not a serializable tuple origin: ${text}`);
  }
  const schemePart = trimmed.slice(0, authorityStart);
  const rest = trimmed.slice(authorityStart + 3);
  const authorityEnd = rest.search(/[/?#]/);
  const authority = authorityEnd === -1 ? rest : rest.slice(0, authorityEnd);
  const match = /^(\[[^\]]+\]|[^:]+)(?::(\d+))?$/.exec(authority);
  if (match === null) {
    throw new TypeError(`not a serializable tuple origin: ${text}`);
  }
  const [, host, portText] = match;
  const scheme = schemePart;
  const port = portText === undefined ? undefined : Number(portText);
  return tupleOrigin(scheme, host, port);
}

/**
 * 来源的结构化克隆。
 *
 * 普通来源复制为相等的新元组；不透明来源保留同一身份令牌，因此克隆体与
 * 原对象同源（用于 postMessage 跨上下文传递来源对象的场景）。
 */
export function cloneOrigin(origin: Origin): Origin {
  if (origin.kind === "opaque") {
    const copy: OpaqueOrigin = Object.freeze({ ...origin });
    const identity = opaqueIdentities.get(origin);
    if (identity !== undefined) {
      opaqueIdentities.set(copy, identity);
    }
    return copy;
  }
  return tupleOrigin(origin.scheme, origin.host, origin.port);
}
