import { randomUUID } from "node:crypto";

/**
 * Connect 错误出口拦截器。
 *
 * 防线：
 * 1. code 为 unknown / internal 的错误：message 替换为固定文案 + 随机关联 ID，
 *    原始 message / stack 只进内部日志，details 全部丢弃。
 * 2. 可展示 code（invalid_argument、not_found 等）：保留 message，但打码
 *    IPv4 地址、`user:pass@` 凭据、文件绝对路径。
 * 3. details（google.protobuf.Any）：type URL 必须在调用方白名单内，否则整条丢弃；
 *    保留下来的 details 编码总长超过 maxDetailBytes（默认 4096）时全部丢弃。
 * 4. 响应 header / trailer / 错误 metadata 中 authorization、cookie、set-cookie
 *    以及 x-internal- 开头的键一律删除。
 */

export interface AnyDetail {
  typeUrl: string;
  value: Uint8Array;
}

export interface Logger {
  error(message: string, meta?: Record<string, unknown>): void;
}

export interface ErrorEgressOptions {
  /** 允许外发的 details type URL 白名单（完整 type URL，如 "acme.v1.FieldViolation"）。 */
  allowedDetailTypes?: readonly string[];
  /** details 编码总长上限（字节），超过则全部丢弃。默认 4096。 */
  maxDetailBytes?: number;
  /** 内部错误对外固定文案（不含关联 ID）。 */
  internalMessage?: string;
  /** 关联 ID 生成器，默认 randomUUID。 */
  idGenerator?: () => string;
  /** 内部日志出口，默认 console。 */
  logger?: Logger;
}

interface ResolvedOptions {
  allowedDetailTypes: ReadonlySet<string>;
  maxDetailBytes: number;
  internalMessage: string;
  idGenerator: () => string;
  logger: Logger;
}

const INTERNAL_CODES: ReadonlySet<string> = new Set(["unknown", "internal"]);
const DEFAULT_MAX_DETAIL_BYTES = 4096;
const DEFAULT_INTERNAL_MESSAGE = "Internal server error";

const FORBIDDEN_METADATA_KEYS: ReadonlySet<string> = new Set([
  "authorization",
  "cookie",
  "set-cookie",
]);
const FORBIDDEN_METADATA_PREFIX = "x-internal-";

/** connect-es 的 Code 是数字枚举，这里同时兼容数字与字符串形式。 */
const NUMERIC_CODE_NAMES: Readonly<Record<number, string>> = {
  1: "canceled",
  2: "unknown",
  3: "invalid_argument",
  4: "deadline_exceeded",
  5: "not_found",
  6: "already_exists",
  7: "permission_denied",
  8: "resource_exhausted",
  9: "failed_precondition",
  10: "aborted",
  11: "out_of_range",
  12: "unimplemented",
  13: "internal",
  14: "unavailable",
  15: "data_loss",
  16: "unauthenticated",
};

const textEncoder = new TextEncoder();

const defaultLogger: Logger = {
  error(message, meta) {
    console.error(message, meta ?? "");
  },
};

function resolveOptions(options: ErrorEgressOptions): ResolvedOptions {
  return {
    allowedDetailTypes: new Set(options.allowedDetailTypes ?? []),
    maxDetailBytes: options.maxDetailBytes ?? DEFAULT_MAX_DETAIL_BYTES,
    internalMessage: options.internalMessage ?? DEFAULT_INTERNAL_MESSAGE,
    idGenerator: options.idGenerator ?? (() => randomUUID()),
    logger: options.logger ?? defaultLogger,
  };
}

function codeName(code: unknown): string | undefined {
  if (typeof code === "string") return code.toLowerCase();
  if (typeof code === "number") return NUMERIC_CODE_NAMES[code];
  return undefined;
}

// ---- message 打码 ----------------------------------------------------------

const CREDENTIAL_RE = /[^\s/:@]+:[^\s/@]+@/g;
const IPV4_RE = /\b(?:\d{1,3}\.){3}\d{1,3}\b/g;
const WINDOWS_PATH_RE = /\b[A-Za-z]:\\(?:[^\s\\/]+\\?)+/g;
// 负向后行断言避免误伤 URL 的 "://host" 部分。
const UNIX_PATH_RE = /(?<![:\w/])\/(?:[\w.@+~-]+\/)+[\w.@+~-]*/g;

/** 打码 message 中的 IPv4、`user:pass@`、文件绝对路径。 */
export function redactMessage(message: string): string {
  return message
    .replace(CREDENTIAL_RE, "***:***@")
    .replace(IPV4_RE, "[redacted-ip]")
    .replace(WINDOWS_PATH_RE, "[redacted-path]")
    .replace(UNIX_PATH_RE, "[redacted-path]");
}

// ---- details 过滤 ----------------------------------------------------------

/**
 * 按白名单过滤 details；保留下来的条目编码总长超过 maxDetailBytes 时全部丢弃。
 */
export function sanitizeDetails(
  details: unknown,
  allowedDetailTypes: ReadonlySet<string>,
  maxDetailBytes: number = DEFAULT_MAX_DETAIL_BYTES,
): AnyDetail[] {
  if (!Array.isArray(details)) return [];
  const kept: AnyDetail[] = [];
  let totalBytes = 0;
  for (const item of details) {
    if (typeof item !== "object" || item === null) continue;
    const detail = item as Partial<AnyDetail>;
    if (typeof detail.typeUrl !== "string") continue;
    if (!allowedDetailTypes.has(detail.typeUrl)) continue;
    const valueLength =
      detail.value instanceof Uint8Array ? detail.value.byteLength : 0;
    totalBytes += textEncoder.encode(detail.typeUrl).byteLength + valueLength;
    kept.push({ typeUrl: detail.typeUrl, value: detail.value ?? new Uint8Array(0) });
  }
  if (totalBytes > maxDetailBytes) return [];
  return kept;
}

// ---- metadata 过滤 ----------------------------------------------------------

export function isForbiddenMetadataKey(key: string): boolean {
  const lower = key.toLowerCase();
  return (
    FORBIDDEN_METADATA_KEYS.has(lower) || lower.startsWith(FORBIDDEN_METADATA_PREFIX)
  );
}

/** 原地删除 Headers 或普通对象中的敏感键。 */
export function sanitizeMetadata(metadata: unknown): void {
  if (metadata === null || typeof metadata !== "object") return;
  const candidate = metadata as {
    forEach?: unknown;
    delete?: unknown;
  };
  if (
    typeof candidate.forEach === "function" &&
    typeof candidate.delete === "function"
  ) {
    const headers = metadata as Headers;
    const doomed: string[] = [];
    headers.forEach((_value, key) => {
      if (isForbiddenMetadataKey(key)) doomed.push(key);
    });
    for (const key of doomed) headers.delete(key);
    return;
  }
  const record = metadata as Record<string, unknown>;
  for (const key of Object.keys(record)) {
    if (isForbiddenMetadataKey(key)) delete record[key];
  }
}

// ---- 错误清洗 ---------------------------------------------------------------

interface ErrorLike {
  code?: unknown;
  message?: unknown;
  details?: unknown;
  metadata?: unknown;
  stack?: unknown;
}

function cloneError(err: object): ErrorLike {
  const clone = Object.assign(Object.create(Object.getPrototypeOf(err)), err);
  return clone as ErrorLike;
}

function bareInternalError(resolved: ResolvedOptions, ref: string): Error {
  const err = new Error(`${resolved.internalMessage} (ref: ${ref})`);
  err.name = "ConnectError";
  const target = err as Error & ErrorLike;
  target.code = "internal";
  target.details = [];
  target.metadata = new Headers();
  return err;
}

/** 清洗单个错误对象，返回可安全抛给客户端的副本。 */
export function sanitizeError(
  err: unknown,
  options: ErrorEgressOptions = {},
): unknown {
  const resolved = resolveOptions(options);
  return sanitizeErrorWith(err, resolved);
}

function sanitizeErrorWith(err: unknown, resolved: ResolvedOptions): unknown {
  if (typeof err !== "object" || err === null) {
    const ref = resolved.idGenerator();
    resolved.logger.error(
      `connect egress: non-object error thrown (ref: ${ref})`,
      { ref, thrown: String(err) },
    );
    return bareInternalError(resolved, ref);
  }

  const source = err as ErrorLike;
  const name = codeName(source.code);
  const originalMessage =
    typeof source.message === "string"
      ? source.message
      : String(source.message ?? "");

  const out = cloneError(err);

  if (name === undefined || INTERNAL_CODES.has(name)) {
    const ref = resolved.idGenerator();
    resolved.logger.error(
      `connect egress: suppressed internal error (ref: ${ref})`,
      {
        ref,
        code: name ?? "unknown",
        message: originalMessage,
        stack: typeof source.stack === "string" ? source.stack : undefined,
      },
    );
    out.message = `${resolved.internalMessage} (ref: ${ref})`;
    out.details = [];
  } else {
    out.message = redactMessage(originalMessage);
    out.details = sanitizeDetails(
      source.details,
      resolved.allowedDetailTypes,
      resolved.maxDetailBytes,
    );
  }

  if (out.metadata !== undefined) sanitizeMetadata(out.metadata);
  return out;
}

// ---- 拦截器 -----------------------------------------------------------------

type UnaryFn = (req: unknown) => Promise<unknown>;

function isAsyncIterable(value: unknown): value is AsyncIterable<unknown> {
  return (
    typeof value === "object" &&
    value !== null &&
    Symbol.asyncIterator in (value as object)
  );
}

function wrapMessageStream(
  stream: AsyncIterable<unknown>,
  resolved: ResolvedOptions,
): AsyncIterable<unknown> {
  return (async function* () {
    try {
      for await (const message of stream) {
        yield message;
      }
    } catch (err) {
      throw sanitizeErrorWith(err, resolved);
    }
  })();
}

/**
 * 创建可导入的 Connect 拦截器。结构上兼容 connect-es 的 Interceptor 类型：
 *
 *   const server = connectNodeAdapter({
 *     routes(router) { ... },
 *     interceptors: [createErrorEgressInterceptor({ allowedDetailTypes: [...] })],
 *   });
 */
export function createErrorEgressInterceptor(
  options: ErrorEgressOptions = {},
): (next: UnaryFn) => UnaryFn {
  const resolved = resolveOptions(options);
  return function errorEgressInterceptor(next: UnaryFn): UnaryFn {
    return async function (req: unknown) {
      try {
        const res = (await next(req)) as {
          header?: unknown;
          trailer?: unknown;
          message?: unknown;
        } | null;
        if (res !== null && typeof res === "object") {
          sanitizeMetadata(res.header);
          sanitizeMetadata(res.trailer);
          if (isAsyncIterable(res.message)) {
            res.message = wrapMessageStream(res.message, resolved);
          }
        }
        return res;
      } catch (err) {
        throw sanitizeErrorWith(err, resolved);
      }
    };
  };
}
