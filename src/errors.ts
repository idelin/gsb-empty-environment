/** 解码错误：携带字节偏移，用于安全越界防护报告。 */
export class ProtobufDecodeError extends Error {
  readonly offset: number;
  constructor(message: string, offset: number) {
    super(`${message} (at byte offset ${offset})`);
    this.name = 'ProtobufDecodeError';
    this.offset = offset;
  }
}

/** 编码错误。 */
export class ProtobufEncodeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ProtobufEncodeError';
  }
}
