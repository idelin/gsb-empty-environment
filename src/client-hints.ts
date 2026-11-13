/**
 * Client Hints 请求附加包。
 *
 * 在请求发出前调用 {@link ClientHintManager#attachClientHints}，依据：
 *  - 顶层 HTTPS 导航响应中的 `Accept-CH` 声明（按顶层来源持久化）；
 *  - 顶层文档 `Permissions-Policy` 中对应 `ch-ua-*` 特性的允许列表；
 * 决定哪些 `Sec-CH-*` 请求头可以随请求发出。
 *
 * 安全模型：
 *  - 低熵提示默认只发给同源请求，跨源必须显式委托；
 *  - 高熵提示必须先被 `Accept-CH` 声明，且同源、或被策略显式委托给目标来源；
 *  - `Accept-CH` 只在 HTTPS 顶层导航响应上生效，子资源响应与明文响应一律忽略；
 *  - Client Hints 不会随非 HTTPS 请求发送。
 */

export type HeaderBag = Record<string, string>;

/** 网络栈在收到响应后提供给本模块的最小信息。 */
export interface ResponseInfo {
  url: string;
  headers: HeaderBag;
  /** 是否为顶层框架导航请求（document 加载），子资源响应传 false。 */
  isTopLevelNavigation: boolean;
}

/** 网络栈在发送请求前提供给本模块的最小信息。 */
export interface RequestInfo {
  url: string;
  /** 发起该请求的文档/框架来源，用于同源判定与策略匹配。 */
  clientOrigin: string;
  headers?: HeaderBag;
}

/** 某个 Permissions-Policy 特性的允许列表。 */
export interface Allowlist {
  /** 列表为 `*`，放行任意来源。 */
  wildcard: boolean;
  /** 列表包含 `self`。 */
  self: boolean;
  /** 列表中显式列出的来源（已规范化为 origin 串）。 */
  origins: string[];
}

/** 默认可在同源请求中发送的低熵提示（小写头名）。 */
export const LOW_ENTROPY_HINTS: ReadonlySet<string> = new Set([
  'sec-ch-ua',
  'sec-ch-ua-mobile',
  'sec-ch-ua-platform',
]);

const CANONICAL_HEADER_NAMES: Readonly<Record<string, string>> = {
  'sec-ch-ua': 'Sec-CH-UA',
  'sec-ch-ua-mobile': 'Sec-CH-UA-Mobile',
  'sec-ch-ua-platform': 'Sec-CH-UA-Platform',
  'sec-ch-ua-arch': 'Sec-CH-UA-Arch',
  'sec-ch-ua-bitness': 'Sec-CH-UA-Bitness',
  'sec-ch-ua-full-version': 'Sec-CH-UA-Full-Version',
  'sec-ch-ua-full-version-list': 'Sec-CH-UA-Full-Version-List',
  'sec-ch-ua-model': 'Sec-CH-UA-Model',
  'sec-ch-ua-platform-version': 'Sec-CH-UA-Platform-Version',
};

const TOKEN_PATTERN = /^[!#$%&'*+\-.^_`|~0-9a-z]+$/;

function getHeader(headers: HeaderBag, name: string): string | undefined {
  const lower = name.toLowerCase();
  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() === lower) {
      return value;
    }
  }
  return undefined;
}

/** 取 URL 的 origin；非法 URL 抛出 TypeError。 */
export function originOf(url: string): string {
  return new URL(url).origin;
}

/** Client Hints 仅允许在安全传输（HTTPS）上发送/声明。 */
export function isSecureUrl(url: string): boolean {
  return new URL(url).protocol === 'https:';
}

/**
 * 把 Client Hint 头名映射为 Permissions-Policy 特性名。
 * 例如 `Sec-CH-UA-Full-Version-List` -> `ch-ua-full-version-list`。
 */
export function hintToFeature(hintName: string): string {
  return hintName
    .toLowerCase()
    .replace(/^sec-/, '')
    .replace(/[^a-z0-9]+/g, '-');
}

/** 输出时使用的标准头名，未知头名退化为按分段首字母大写。 */
export function canonicalHeaderName(name: string): string {
  const lower = name.toLowerCase();
  const known = CANONICAL_HEADER_NAMES[lower];
  if (known) {
    return known;
  }
  return lower
    .split('-')
    .map((part) => (part ? part[0].toUpperCase() + part.slice(1) : part))
    .join('-');
}

/**
 * 解析 `Accept-CH` 响应头（逗号分隔的字段名令牌）。
 * 非法条目被忽略，结果去重并转为小写。
 */
export function parseAcceptCh(raw: string): string[] {
  const hints: string[] = [];
  for (const piece of raw.split(',')) {
    const token = piece.trim().toLowerCase();
    if (token && TOKEN_PATTERN.test(token) && !hints.includes(token)) {
      hints.push(token);
    }
  }
  return hints;
}

/** 在尊重括号与引号嵌套的前提下，按顶层逗号切分指令。 */
function splitDirectives(raw: string): string[] {
  const directives: string[] = [];
  let depth = 0;
  let inQuote = false;
  let escaped = false;
  let start = 0;
  for (let index = 0; index < raw.length; index += 1) {
    const char = raw[index];
    if (inQuote) {
      if (escaped) {
        escaped = false;
      } else if (char === '\\') {
        escaped = true;
      } else if (char === '"') {
        inQuote = false;
      }
      continue;
    }
    if (char === '"') {
      inQuote = true;
    } else if (char === '(') {
      depth += 1;
    } else if (char === ')') {
      depth = Math.max(0, depth - 1);
    } else if (char === ',' && depth === 0) {
      directives.push(raw.slice(start, index));
      start = index + 1;
    }
  }
  directives.push(raw.slice(start));
  return directives.map((item) => item.trim()).filter(Boolean);
}

/**
 * 解析一个或多个 `Permissions-Policy` 响应头（Structured Headers 字典）。
 * 多个头中的同名特性按并集合并；无法识别的指令/令牌忽略。
 */
export function parsePermissionsPolicy(
  raw: string | readonly string[],
): Map<string, Allowlist> {
  const policies = new Map<string, Allowlist>();
  const headerValues = Array.isArray(raw) ? raw : [raw];
  const tokenPattern = /"((?:[^"\\]|\\.)*)"|([!#$%&'*+\-.^_`|~0-9a-zA-Z]+)/g;

  for (const headerValue of headerValues) {
    for (const directive of splitDirectives(headerValue)) {
      const equals = directive.indexOf('=');
      if (equals < 0) {
        continue;
      }
      const feature = directive.slice(0, equals).trim().toLowerCase();
      if (!TOKEN_PATTERN.test(feature)) {
        continue;
      }
      const value = directive.slice(equals + 1).trim();
      const allowlist: Allowlist = { wildcard: false, self: false, origins: [] };

      if (value === '*') {
        allowlist.wildcard = true;
      } else if (value.startsWith('(') && value.endsWith(')')) {
        const inner = value.slice(1, -1);
        for (const match of inner.matchAll(tokenPattern)) {
          const quoted = match[1];
          const bare = match[2];
          if (quoted !== undefined) {
            try {
              const origin = new URL(quoted).origin;
              if (!allowlist.origins.includes(origin)) {
                allowlist.origins.push(origin);
              }
            } catch {
              // 非法来源字符串忽略。
            }
          } else if (bare === '*') {
            allowlist.wildcard = true;
          } else if (bare === 'self') {
            allowlist.self = true;
          }
          // `src` 等其它令牌需要框架上下文才能解析，此处不参与匹配。
        }
      } else {
        continue; // 畸形指令，整条忽略。
      }

      const existing = policies.get(feature);
      if (!existing) {
        policies.set(feature, allowlist);
      } else {
        existing.wildcard ||= allowlist.wildcard;
        existing.self ||= allowlist.self;
        for (const origin of allowlist.origins) {
          if (!existing.origins.includes(origin)) {
            existing.origins.push(origin);
          }
        }
      }
    }
  }
  return policies;
}

export interface ClientHintManagerOptions {
  /** 本机可提供的全部提示值，键为头名（大小写不敏感）。 */
  hintValues: HeaderBag;
  /** 顶层文档的 Permissions-Policy 响应头（可传多个头的值）。 */
  permissionsPolicy?: string | readonly string[];
}

/**
 * Client Hints 状态机：记录各顶层来源通过 `Accept-CH` 声明的提示，
 * 并在请求发出前按同源/委托规则附加提示头。
 */
export class ClientHintManager {
  readonly #hintValues: Map<string, string>;
  readonly #policy: Map<string, Allowlist>;
  readonly #acceptedHints = new Map<string, Set<string>>();

  constructor(options: ClientHintManagerOptions) {
    this.#hintValues = new Map(
      Object.entries(options.hintValues).map(([name, value]) => [
        name.toLowerCase(),
        value,
      ]),
    );
    this.#policy = parsePermissionsPolicy(options.permissionsPolicy ?? []);
  }

  /**
   * 观察每个响应。仅当响应是 HTTPS 顶层导航响应时，其中的 `Accept-CH`
   * 才会生效并按其来源记录；子资源响应、明文响应中的声明被忽略。
   */
  observeResponse(response: ResponseInfo): void {
    if (!response.isTopLevelNavigation || !isSecureUrl(response.url)) {
      return;
    }
    const raw = getHeader(response.headers, 'accept-ch');
    if (raw === undefined) {
      return;
    }
    this.#acceptedHints.set(
      originOf(response.url),
      new Set(parseAcceptCh(raw)),
    );
  }

  /** 返回某顶层来源已通过 `Accept-CH` 声明的提示（小写头名）。 */
  getAcceptedHints(topLevelOrigin: string): string[] {
    const set = this.#acceptedHints.get(originOf(topLevelOrigin));
    return set ? [...set] : [];
  }

  /**
   * 计算一次请求允许携带的 Client Hint 头。
   * 不修改入参，返回新的头集合（键为标准大小写头名）。
 */
  buildRequestHeaders(request: RequestInfo): HeaderBag {
    if (!isSecureUrl(request.url)) {
      return {};
    }
    const targetOrigin = originOf(request.url);
    const clientOrigin = originOf(request.clientOrigin);
    const sameOrigin = targetOrigin === clientOrigin;
    const accepted =
      this.#acceptedHints.get(clientOrigin) ?? new Set<string>();
    const attached: HeaderBag = {};

    for (const [hint, value] of this.#hintValues) {
      if (!LOW_ENTROPY_HINTS.has(hint) && !accepted.has(hint)) {
        continue; // 高熵提示未被该页面的 Accept-CH 声明。
      }
      const feature = hintToFeature(hint);
      if (!this.#featureAllowed(feature, targetOrigin, sameOrigin)) {
        continue; // 跨源且未被 Permissions-Policy 委托给目标来源。
      }
      attached[canonicalHeaderName(hint)] = value;
    }
    return attached;
  }

  /**
   * 请求发出前调用：把允许的 Client Hint 头合并进 `request.headers`
   * （同名头大小写不敏感覆盖），并返回合并后的头集合。
   */
  attachClientHints(request: RequestInfo): HeaderBag {
    const headers = request.headers ?? {};
    request.headers = headers;
    for (const [name, value] of Object.entries(
      this.buildRequestHeaders(request),
    )) {
      setHeaderCaseInsensitive(headers, name, value);
    }
    return headers;
  }

  #featureAllowed(
    feature: string,
    targetOrigin: string,
    sameOrigin: boolean,
  ): boolean {
    const allowlist = this.#policy.get(feature);
    if (!allowlist) {
      return sameOrigin; // 特性未出现时默认允许列表等价于 self。
    }
    if (allowlist.wildcard) {
      return true;
    }
    if (allowlist.self && sameOrigin) {
      return true;
    }
    return allowlist.origins.includes(targetOrigin);
  }
}

function setHeaderCaseInsensitive(
  headers: HeaderBag,
  name: string,
  value: string,
): void {
  const lower = name.toLowerCase();
  for (const key of Object.keys(headers)) {
    if (key.toLowerCase() === lower) {
      delete headers[key];
      headers[name] = value;
      return;
    }
  }
  headers[name] = value;
}
