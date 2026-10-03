/** 统一错误体系：所有可预期的解析/容错异常都带结构化上下文。 */

export class WireFormatError extends Error {
  readonly offset: number;
  constructor(message: string, offset: number) {
    super(`${message} (offset=${offset})`);
    this.name = 'WireFormatError';
    this.offset = offset;
  }
}

/** 字段截断 / 安全越界防护触发时抛出。 */
export class TruncatedFieldError extends WireFormatError {
  readonly needed: number;
  readonly remaining: number;
  constructor(needed: number, remaining: number, offset: number, what = 'field') {
    super(
      `安全越界防护: ${what} 需要 ${needed} 字节, 缓冲区仅剩 ${remaining} 字节`,
      offset,
    );
    this.name = 'TruncatedFieldError';
    this.needed = needed;
    this.remaining = remaining;
  }
}

/** Varint 超过 10 字节(64 位上限)或缺少终止字节。 */
export class MalformedVarintError extends WireFormatError {
  constructor(offset: number) {
    super('安全越界防护: varint 超过 10 字节仍未终止(疑似畸形/截断数据)', offset);
    this.name = 'MalformedVarintError';
  }
}

/** Schema 反射校验失败。 */
export class SchemaValidationError extends Error {
  readonly fieldPath: string;
  constructor(message: string, fieldPath: string) {
    super(`${message} (path=${fieldPath})`);
    this.name = 'SchemaValidationError';
    this.fieldPath = fieldPath;
  }
}

/** 注入的故障(断电 / 磁盘满 / 丢包)。 */
export class InjectedFaultError extends Error {
  readonly faultKind: 'power-loss' | 'disk-full' | 'packet-loss';
  constructor(kind: 'power-loss' | 'disk-full' | 'packet-loss', detail: string) {
    super(`[fault-injection:${kind}] ${detail}`);
    this.name = 'InjectedFaultError';
    this.faultKind = kind;
  }
}

/** 日志记录校验和损坏。 */
export class ChecksumMismatchError extends Error {
  constructor(where: string) {
    super(`CRC32 校验失败: ${where}`);
    this.name = 'ChecksumMismatchError';
  }
}
