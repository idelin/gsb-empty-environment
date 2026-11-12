/**
 * RFC 6455 §4.2 Sec-WebSocket-Protocol 握手协商（纯函数、零依赖）。
 *
 * 关键安全约定：
 * - 服务端只在客户端列表与自身列表的交集中，按服务端优先级选择一个子协议；
 * - 选中值使用客户端提交时的原始拼写（token 大小写敏感）原样回显；
 * - 无交集时不输出 Sec-WebSocket-Protocol，握手继续，subprotocol 为 null；
 * - 元素必须是 RFC 7230 token 字符，出现重复名直接拒绝握手；
 * - `bearer.<token>` 仅用于提取令牌，永远不会进入响应头、审计信息或日志。
 */

/** bearer 元素前缀，例如 `bearer.eyJhbGci...`。 */
export const BEARER_PREFIX = "bearer.";

/** RFC 7230 token = 1*tchar；RFC 6455 §11.5 要求子协议名是 token。 */
const TOKEN_RE = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;

/** 判断一个字符串是否为合法 token（非空且只含 tchar）。 */
export function isToken(value: string): boolean {
  return TOKEN_RE.test(value);
}

/**
 * 对可能出现在日志里的 bearer 令牌做脱敏。
 * 非 token 输入保持原样，便于排查协议错误且不泄露内容。
 */
export function redactBearer(value: string): string {
  if (value.startsWith(BEARER_PREFIX)) {
    const secret = value.slice(BEARER_PREFIX.length);
    return `${BEARER_PREFIX}${isToken(secret) && secret.length > 0 ? "[REDACTED]" : secret}`;
  }
  return value;
}

/**
 * 解析整个 Sec-WebSocket-Protocol 头：逗号分隔，忽略每个元素前后的 OWS。
 * 空白会被 trim，但空元素（如 `a,,b`）会保留下来交由协商逻辑拒绝，
 * 避免把畸形头静默当成合法输入接受。
 */
export function splitProtocolHeader(header: string | null | undefined): string[] {
  if (header == null) return [];
  return header.split(",").map((part) => part.trim());
}

/**
 * 从请求头里提取的 bearer 令牌。
 * 令牌保存在非枚举私有字段中：JSON.stringify / console.log 只显示 "[REDACTED]"。
 */
export class BearerToken {
  readonly #value: string;

  constructor(value: string) {
    this.#value = value;
  }

  /** 仅在确有需要（例如校验签名）时显式读取原始令牌。 */
  reveal(): string {
    return this.#value;
  }

  toJSON(): { bearerToken: string } {
    return { bearerToken: "[REDACTED]" };
  }

  toString(): string {
    return "[REDACTED]";
  }

  readonly [Symbol.for("nodejs.util.inspect.custom")] = (): string =>
    "BearerToken([REDACTED])";
}

/** 握手成功时的审计/日志视图（保证不含原始令牌）。 */
export interface NegotiationAudit {
  /** 脱敏后的客户端原始元素列表，可直接写日志。 */
  offered: string[];
  /** 被服务端选中的子协议；无交集时为 null。 */
  selected: string | null;
  /** 本次握手是否夹带了 bearer 令牌（只记录布尔值，不记录令牌）。 */
  hasBearerToken: boolean;
}

/** 握手协商结果。 */
export type NegotiationResult =
  | {
      ok: true;
      /** 选中的子协议；无交集时为 null，调用方不得输出该响应头。 */
      subprotocol: string | null;
      /** 从 `bearer.<token>` 元素提取的令牌；不存在时为 null。 */
      bearerToken: BearerToken | null;
      /** 日志安全的审计视图。 */
      audit: NegotiationAudit;
      /** 生成响应头；无选中子协议时返回空对象。 */
      toResponseHeaders(): Record<string, string>;
    }
  | {
      ok: false;
      /** 机器可读的拒绝原因，内容不含任何令牌。 */
      reason:
        | "empty-element"
        | "invalid-token"
        | "duplicate-element"
        | "multiple-bearer-tokens"
        | "empty-bearer-token";
      /** 日志安全的审计视图。 */
      audit: NegotiationAudit;
    };

export interface NegotiateOptions {
  /** 服务端支持的子协议，按服务端优先级从高到低排列。 */
  serverProtocols: readonly string[];
}

function buildAudit(offered: string[], selected: string | null, hasBearerToken: boolean): NegotiationAudit {
  return {
    offered: offered.map(redactBearer),
    selected,
    hasBearerToken,
  };
}

/**
 * 执行 RFC 6455 §4.2 子协议协商。
 *
 * @param header 原始 Sec-WebSocket-Protocol 请求头（未发送该头时传 null/undefined）
 * @param options 服务端优先级列表等选项
 */
export function negotiateSubprotocol(
  header: string | null | undefined,
  options: NegotiateOptions,
): NegotiationResult {
  const { serverProtocols } = options;
  if (serverProtocols.length === 0) {
    throw new Error("negotiateSubprotocol: serverProtocols must not be empty");
  }
  for (const protocol of serverProtocols) {
    if (!isToken(protocol) || protocol.startsWith(BEARER_PREFIX)) {
      throw new Error(
        `negotiateSubprotocol: server protocol "${redactBearer(protocol)}" is not a valid non-bearer token`,
      );
    }
  }

  const offered = splitProtocolHeader(header);
  const elements = offered;

  const seen = new Set<string>();
  let bearerToken: BearerToken | null = null;
  const clientProtocols: string[] = [];

  for (const element of elements) {
    if (seen.has(element)) {
      return { ok: false, reason: "duplicate-element", audit: buildAudit(offered, null, bearerToken !== null) };
    }
    seen.add(element);

    if (element.length === 0) {
      return {
        ok: false,
        reason: "empty-element",
        audit: buildAudit(offered, null, bearerToken !== null),
      };
    }

    if (element.startsWith(BEARER_PREFIX)) {
      if (bearerToken !== null) {
        return { ok: false, reason: "multiple-bearer-tokens", audit: buildAudit(offered, null, true) };
      }
      const secret = element.slice(BEARER_PREFIX.length);
      if (secret.length === 0 || !isToken(secret)) {
        return { ok: false, reason: "empty-bearer-token", audit: buildAudit(offered, null, false) };
      }
      bearerToken = new BearerToken(secret);
      continue;
    }

    if (!isToken(element)) {
      return {
        ok: false,
        reason: "invalid-token",
        audit: buildAudit(offered, null, bearerToken !== null),
      };
    }
    clientProtocols.push(element);
  }

  const clientSet = new Set(clientProtocols);
  let selected: string | null = null;
  for (const serverProtocol of serverProtocols) {
    if (clientSet.has(serverProtocol)) {
      selected = serverProtocol;
      break;
    }
  }

  return {
    ok: true,
    subprotocol: selected,
    bearerToken,
    audit: buildAudit(offered, selected, bearerToken !== null),
    toResponseHeaders() {
      return selected === null ? {} : { "Sec-WebSocket-Protocol": selected };
    },
  };
}

/** 对整段请求头做脱敏，用于在拒绝握手等场景记录原始头。 */
export function safeHeaderForLog(header: string | null | undefined): string {
  if (header == null) return "";
  return splitProtocolHeader(header).map(redactBearer).join(", ");
}
