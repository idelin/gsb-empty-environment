// 所有格式/损坏类错误的统一类型：解码器对外只抛出 FormatError。
export class FormatError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'FormatError';
  }
}
