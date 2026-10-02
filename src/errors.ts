/** 错误类型层次：所有 Wire-format 解析错误均携带偏移量，便于定位截断点。 */

export class WireFormatError extends Error {
  readonly offset: number;
  constructor(message: string, offset: number) {
    super(`${message} (offset=${offset})`);
    this.name = 'WireFormatError';
    this.offset = offset;
  }
}

/** 输入缓冲区在逻辑消息结束前耗尽 —— 安全越界防护的触发点。 */
export class TruncationError extends WireFormatError {
  readonly needed: number;
  readonly available: number;
  constructor(message: string, offset: number, needed: number, available: number) {
    super(message, offset);
    this.name = 'TruncationError';
    this.needed = needed;
    this.available = available;
  }
}

/** 字节流违反 Wire-format 语法（如 varint 超 10 字节、非法 wire type）。 */
export class MalformedError extends WireFormatError {
  constructor(message: string, offset: number) {
    super(message, offset);
    this.name = 'MalformedError';
  }
}

export interface ViolationLike {
  kind: string;
  detail: string;
}

/** 巡检断言失败时抛出，携带完整巡检报告。 */
export class InvariantViolationError extends Error {
  readonly report: unknown;
  constructor(message: string, report: unknown) {
    super(message);
    this.name = 'InvariantViolationError';
    this.report = report;
  }
}
