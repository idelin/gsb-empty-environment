/**
 * package-lock.json（lockfileVersion 3）解析：
 * 从 packages 条目中提取真实包名，并校验 resolved 主机是否在允许表内。
 */

import type {
  LockedPackage,
  LockfilePackageEntry,
  PackageLock,
  ResolvedHostViolation,
} from './types.ts';

/** npm 官方 registry 的默认主机。 */
export const DEFAULT_ALLOWED_HOSTS = ['registry.npmjs.org'];

/**
 * 取路径中最后一个 node_modules 段作为包名。
 * 支持 scoped 包（@scope/name），并兼容 node_modules 被 URL 编码的写法。
 */
export function realNameFromPath(installPath: string): string | undefined {
  const marker = 'node_modules/';
  const index = installPath.lastIndexOf(marker);
  if (index === -1) {
    return undefined;
  }
  let segment = installPath.slice(index + marker.length);
  if (segment === '') {
    return undefined;
  }
  try {
    segment = decodeURIComponent(segment);
  } catch {
    /** 解码失败时保留原始段。 */
  }
  return segment;
}

/**
 * 解析单个 packages 条目的真实名称：
 * name 字段优先（别名安装时 npm 在此写入真实包名），否则取路径最后一个 node_modules 段。
 */
export function resolveRealName(installPath: string, entry: LockfilePackageEntry): string | undefined {
  if (typeof entry.name === 'string' && entry.name.trim() !== '') {
    return entry.name.trim();
  }
  return realNameFromPath(installPath);
}

/**
 * 解析 lockfileVersion 3 的 packages 字典，产出待评估的真实包列表。
 * 根项目（键为 ''）、workspace 链接（link: true）与无版本条目不纳入评估。
 */
export function parseLockedPackages(lock: PackageLock): LockedPackage[] {
  const result: LockedPackage[] = [];
  const packages = lock.packages;
  if (!packages || typeof packages !== 'object') {
    return result;
  }
  for (const [installPath, entry] of Object.entries(packages)) {
    if (installPath === '' || !entry || entry.link === true) {
      continue;
    }
    if (typeof entry.version !== 'string' || entry.version.trim() === '') {
      continue;
    }
    const realName = resolveRealName(installPath, entry);
    if (!realName) {
      continue;
    }
    const pathName = realNameFromPath(installPath);
    result.push({
      realName,
      version: entry.version.trim(),
      installPath,
      declaredName: typeof entry.name === 'string' ? entry.name.trim() : undefined,
      resolved: typeof entry.resolved === 'string' ? entry.resolved : undefined,
      aliased: pathName !== undefined && pathName !== realName,
    });
  }
  return result;
}

/**
 * 校验单条 resolved 的主机是否在允许表内。
 * 返回违规报告；合法则返回 undefined。
 */
export function checkResolvedHost(
  pkg: LockedPackage,
  allowedHosts: string[],
): ResolvedHostViolation | undefined {
  const base: Omit<ResolvedHostViolation, 'reason' | 'host'> = {
    kind: 'resolved-host',
    installPath: pkg.installPath,
    realName: pkg.realName,
    version: pkg.version,
    resolved: pkg.resolved,
    allowedHosts: [...allowedHosts],
  };

  if (!pkg.resolved) {
    return { ...base, reason: 'missing-resolved' };
  }

  let url: URL;
  try {
    url = new URL(pkg.resolved);
  } catch {
    return { ...base, reason: 'unparsable-resolved' };
  }

  const host = url.hostname.toLowerCase();
  const allowed = allowedHosts.map((item) => item.toLowerCase());
  if (!allowed.includes(host)) {
    return { ...base, reason: 'disallowed-host', host };
  }
  return undefined;
}

/** 对全部锁定包执行 resolved 主机校验。 */
export function checkAllResolvedHosts(
  packages: LockedPackage[],
  allowedHosts: string[] = DEFAULT_ALLOWED_HOSTS,
): ResolvedHostViolation[] {
  const violations: ResolvedHostViolation[] = [];
  for (const pkg of packages) {
    const violation = checkResolvedHost(pkg, allowedHosts);
    if (violation) {
      violations.push(violation);
    }
  }
  return violations;
}
