export const MIN_FILTERING_ID = 0;
export const MAX_FILTERING_ID = 255;
export const MAX_BUCKET = 1n << 128n;
export const DEFAULT_FILTERING_ID = 0;

/**
 * 把 origin 字符串规范化为可比较的 origin 元组。
 * 仅接受 http/https 方案，避免 data:、file:、javascript: 等来源混入白名单。
 */
export function normalizeOrigin(rawOrigin: unknown): string {
  if (typeof rawOrigin !== 'string' || rawOrigin.length === 0) {
    throw new TypeError('origin must be a non-empty string');
  }
  let url: URL;
  try {
    url = new URL(rawOrigin);
  } catch {
    throw new TypeError(`invalid origin: ${rawOrigin}`);
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') {
    throw new TypeError(`origin must use http(s) scheme: ${rawOrigin}`);
  }
  if (url.username !== '' || url.password !== '') {
    throw new TypeError(`origin must not contain credentials: ${rawOrigin}`);
  }
  const origin = url.origin;
  if (origin === 'null' || !/^https?:\/\/[^/]+$/.test(origin)) {
    throw new TypeError(`invalid origin: ${rawOrigin}`);
  }
  return origin;
}

export function buildOriginWhitelist(
  rawOrigins: readonly string[],
): ReadonlySet<string> {
  if (!Array.isArray(rawOrigins)) {
    throw new TypeError('allowedCoordinatorOrigins must be an array');
  }
  return new Set(rawOrigins.map((entry) => normalizeOrigin(entry)));
}

function isIntegerInRange(value: unknown, min: number, max: number): boolean {
  return (
    typeof value === 'number' &&
    Number.isInteger(value) &&
    value >= min &&
    value <= max
  );
}

/** bucket：bigint 且落在 [0, 2^128)。number 一律拒绝（无法保证 128 位精度）。 */
export function assertValidBucket(bucket: unknown): asserts bucket is bigint {
  if (typeof bucket !== 'bigint') {
    throw new TypeError('bucket must be a bigint');
  }
  if (bucket < 0n || bucket >= MAX_BUCKET) {
    throw new TypeError('bucket must be an integer in [0, 2^128)');
  }
}

/** value：非负整数。负值、浮点、NaN、Infinity、bigint 均拒绝。 */
export function assertValidValue(value: unknown): asserts value is number {
  if (typeof value !== 'number' || !Number.isInteger(value) || value < 0) {
    throw new TypeError('value must be a non-negative integer');
  }
}

export function assertValidFilteringId(value: unknown): asserts value is number {
  if (!isIntegerInRange(value, MIN_FILTERING_ID, MAX_FILTERING_ID)) {
    throw new TypeError('filtering_id must be an integer in [0, 255]');
  }
}

/** context_id：字符串且 UTF-8 编码后不超过 maxBytes 字节。 */
export function assertValidContextId(
  contextId: unknown,
  maxBytes: number,
): asserts contextId is string {
  if (typeof contextId !== 'string' || contextId.length === 0) {
    throw new TypeError('context_id must be a non-empty string');
  }
  const byteLength = new TextEncoder().encode(contextId).length;
  if (byteLength > maxBytes) {
    throw new TypeError(
      `context_id must be at most ${maxBytes} bytes (got ${byteLength})`,
    );
  }
}
