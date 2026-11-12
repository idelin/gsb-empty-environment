import {
  isForbiddenClass,
  matchesAllowList,
  validateAllowListEntry,
  type ClassNameResult,
  resolveHostClassName,
} from './names.ts';

/**
 * GraalVM 上下文创建选项中与宿主逃逸相关的最小投影。
 * 字段名对应 Polyglot Context.Builder 的常见配置，由配置服务映射填充。
 */
export interface ContextOptions {
  /** 对应 allowAllAccess(true)：聚合开关，打开即获得全部宿主权限。 */
  allowAllAccess?: boolean;
  /** 对应 allowNativeAccess(true)：允许来宾代码加载/调用原生代码。 */
  allowNativeAccess?: boolean;
  /**
   * 宿主访问策略：'all' 等价 HostAccess.ALL（可访问所有宿主类成员），
   * 'none' 等价 HostAccess.NONE，'explicit' 表示仅暴露显式注解成员。
   */
  hostAccess?: 'all' | 'none' | 'explicit';
  /** 对应 allowCreateThread(true)：允许来宾代码创建宿主线程。 */
  allowCreateThread?: boolean;
  /** 资源上限；未设置或未给出有效 statementLimit 视为无上限。 */
  resourceLimits?: ResourceLimits | null;
}

export interface ResourceLimits {
  /** 对应 ResourceLimits.Builder.statementLimit，正数表示有上限。 */
  statementLimit?: number | null;
}

export interface PermissionDecision {
  allowed: boolean;
  reasons: string[];
}

export interface PreflightResult extends PermissionDecision {
  /** 归一化后的有效放行清单（去除空白、去重，保持顺序）。 */
  allowList: string[];
}

/**
 * 上下文创建前的权限校验。
 *
 * 任一硬性条件命中即拒绝：
 *   1. allowAllAccess 开启；
 *   2. allowNativeAccess 开启；
 *   3. hostAccess 选了 'all'；
 *   4. 允许创建线程且未设置资源上限。
 * 同时校验放行清单，禁止包条目与非法条目同样导致拒绝。
 */
export function validateContextPermissions(
  options: ContextOptions,
  hostClassAllowList: readonly string[],
): PreflightResult {
  const reasons: string[] = [];

  if (options.allowAllAccess === true) {
    reasons.push('allowAllAccess 开启时将授予全部宿主访问权限，必须关闭');
  }
  if (options.allowNativeAccess === true) {
    reasons.push('allowNativeAccess 开启时来宾可调用原生代码，必须关闭');
  }
  if (options.hostAccess === 'all') {
    reasons.push("hostAccess 为 'all'（HostAccess.ALL），必须改用 'none' 或 'explicit'");
  }
  if (options.allowCreateThread === true && !hasResourceLimit(options)) {
    reasons.push('允许创建线程时必须设置资源上限（statementLimit）');
  }

  const normalizedAllowList: string[] = [];
  for (const entry of hostClassAllowList) {
    const trimmed = typeof entry === 'string' ? entry.trim() : entry;
    const error = validateAllowListEntry(trimmed);
    if (error !== null) {
      reasons.push(error);
      continue;
    }
    if (!normalizedAllowList.includes(trimmed)) {
      normalizedAllowList.push(trimmed as string);
    }
  }

  return {
    allowed: reasons.length === 0,
    reasons,
    allowList: normalizedAllowList,
  };
}

/**
 * 抛异常版本，便于配置服务在上下文创建前直接 fail-fast。
 */
export function assertContextAllowed(
  options: ContextOptions,
  hostClassAllowList: readonly string[],
): void {
  const decision = validateContextPermissions(options, hostClassAllowList);
  if (!decision.allowed) {
    throw new PermissionDeniedError(decision.reasons);
  }
}

export class PermissionDeniedError extends Error {
  readonly reasons: readonly string[];

  constructor(reasons: readonly string[]) {
    super(`GraalVM 上下文权限校验失败: ${reasons.join('; ')}`);
    this.name = 'PermissionDeniedError';
    this.reasons = [...reasons];
  }
}

function hasResourceLimit(options: ContextOptions): boolean {
  const limit = options.resourceLimits?.statementLimit;
  return typeof limit === 'number' && Number.isFinite(limit) && limit > 0;
}

/**
 * 单次宿主类查询的裁决结果，供配置服务包装 Java.type 闸门时使用。
 */
export function checkHostClassQuery(
  rawName: string,
  allowList: readonly string[],
): PermissionDecision & { resolved?: string } {
  const resolved: ClassNameResult = resolveHostClassName(rawName);
  if (!resolved.ok) {
    return { allowed: false, reasons: [resolved.reason] };
  }

  const reasons: string[] = [];
  if (isForbiddenClass(resolved.canonical)) {
    reasons.push(`禁止包中的宿主类永不放行: ${resolved.canonical}`);
  }

  if (!matchesAllowList(resolved.canonical, allowList)) {
    reasons.push(`宿主类不在放行清单内: ${resolved.canonical}`);
  }

  const allowed = reasons.length === 0;
  return allowed
    ? { allowed: true, reasons: [], resolved: resolved.canonical }
    : { allowed: false, reasons, resolved: resolved.canonical };
}
