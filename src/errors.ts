/** 统一错误码：解析类 + 容错类。 */
export type ErrorCode =
  | 'TRUNCATED'        // 缓冲区在字段中途耗尽（越界防护）
  | 'OVERFLOW'         // varint 超过 64 位（>10 字节）
  | 'INVALID_WIRE_TYPE'
  | 'INVALID_TAG'
  | 'SCHEMA_VIOLATION' // 反射校验失败（类型/必填/包格式）
  | 'CHECKSUM_MISMATCH'
  | 'POWER_LOSS'       // 注入：突发断电
  | 'DISK_FULL'        // 注入：磁盘满
  | 'PACKET_LOSS';     // 注入：网络丢包

export class ProtoError extends Error {
  readonly code: ErrorCode;
  /** 出错时在输入缓冲区中的字节偏移（若可知）。 */
  readonly offset: number;
  /** 字段路径，如 "order.items[2].price"。 */
  readonly path: string;

  constructor(code: ErrorCode, message: string, offset = -1, path = '') {
    super(`[${code}]${path ? ` ${path}` : ''}${offset >= 0 ? ` @byte ${offset}` : ''}: ${message}`);
    this.name = 'ProtoError';
    this.code = code;
    this.offset = offset;
    this.path = path;
  }
}

export function isProtoError(err: unknown, code?: ErrorCode): err is ProtoError {
  return err instanceof ProtoError && (code === undefined || err.code === code);
}
