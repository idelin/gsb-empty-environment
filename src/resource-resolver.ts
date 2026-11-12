/**
 * 调用方提供的同包资源解析器。
 *
 * 预处理器只把通过安全归一化的“包根相对资源键”传给解析器；
 * 解析器自身再做一次登记校验，未登记资源返回 null（或抛错），
 * 因此即使归一化逻辑被绕过，解析器仍是最后一道防线。
 */

export interface ResolveContext {
  /** 归一化后的包根相对资源键，例如 fragments/header.xml */
  readonly key: string;
  /** 触发解析的原始 href（仅用于日志/审计） */
  readonly href: string;
  /** 发起包含的文档资源键 */
  readonly fromResource: string;
  /** 当前嵌套深度（根文档为 0） */
  readonly depth: number;
}

export interface ResourceResolver {
  /** 返回 UTF-8 字符串资源；未登记/不可见时返回 null */
  resolve(context: ResolveContext): Promise<string | null> | string | null;
}

/** 基于内存 Map 的同包资源登记实现，方便转换服务直接使用或在测试中计数。 */
export class MapResourceResolver implements ResourceResolver {
  private readonly resources: ReadonlyMap<string, string>;
  readonly callLog: ResolveContext[] = [];
  callCount = 0;

  constructor(entries: Record<string, string> | ReadonlyMap<string, string>) {
    this.resources =
      entries instanceof Map ? new Map(entries) : new Map(Object.entries(entries));
  }

  resolve(context: ResolveContext): string | null {
    this.callCount += 1;
    this.callLog.push(context);
    return this.resources.has(context.key)
      ? this.resources.get(context.key)!
      : null;
  }
}
