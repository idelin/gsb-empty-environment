/**
 * 错误类型分级：
 * - XIncludeSecurityError：违反安全策略（越权 href、非法 parse、xml:base 越界等），
 *   属于调用方配置/攻击行为，xi:fallback 不得吞掉这类错误。
 * - XIncludeResourceError：资源本身无法解析（未登记、循环、过深等），允许 fallback 兜底。
 * - XIncludeLimitError：全局展开字节数超限，属于整体处理失败，立即中止。
 */

export class XIncludeError extends Error {
  override readonly name = "XIncludeError";
}

export class XIncludeSecurityError extends XIncludeError {
  override readonly name = "XIncludeSecurityError";
}

export class XIncludeResourceError extends XIncludeError {
  override readonly name = "XIncludeResourceError";
}

export class XIncludeCycleError extends XIncludeResourceError {
  override readonly name = "XIncludeCycleError";
  readonly chain: readonly string[];

  constructor(chain: readonly string[]) {
    super(
      `XInclude 循环引用: ${chain.join(" -> ")}`,
    );
    this.chain = chain;
  }
}

export class XIncludeLimitError extends XIncludeError {
  override readonly name = "XIncludeLimitError";
}

export class XmlParseError extends Error {
  override readonly name = "XmlParseError";
  readonly position: number;

  constructor(
    message: string,
    position: number,
  ) {
    super(`${message} (位置 ${position})`);
    this.position = position;
  }
}
