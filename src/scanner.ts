/**
 * 发布门禁调用的锁定文件闭合扫描入口。
 */

import {
  checkAllResolvedHosts,
  DEFAULT_ALLOWED_HOSTS,
  parseLockedPackages,
} from './lockfile.ts';
import { evaluateAdvisory } from './osv.ts';
import type {
  LockfileClosureResult,
  LockedPackage,
  OsvAdvisory,
  PackageEvaluation,
  PackageLock,
  ScanOptions,
} from './types.ts';

const SUPPORTED_LOCKFILE_VERSION = 3;

/** 解析 package-lock.json 文本；JSON 非法时抛出错误供门禁捕获。 */
export function parseLockfileText(text: string): PackageLock {
  const parsed: unknown = JSON.parse(text);
  if (!parsed || typeof parsed !== 'object') {
    throw new Error('lockfile 根节点不是对象');
  }
  return parsed as PackageLock;
}

function evaluatePackage(
  pkg: LockedPackage,
  advisories: Record<string, OsvAdvisory[]>,
): PackageEvaluation {
  const base = {
    realName: pkg.realName,
    version: pkg.version,
    installPath: pkg.installPath,
  };

  if (!Object.hasOwn(advisories, pkg.realName)) {
    return { ...base, affectedBy: [], evaluated: false, reason: 'no-osv-data' };
  }

  const packageAdvisories = advisories[pkg.realName];
  const affectedBy: string[] = [];
  let failureReason: NonNullable<PackageEvaluation['reason']> | undefined;

  for (const advisory of packageAdvisories) {
    const result = evaluateAdvisory(pkg.version, advisory);
    if (result.unevaluable) {
      failureReason ??= result.unevaluableReason;
    }
    if (result.affected) {
      affectedBy.push(advisory.id);
    }
  }

  if (failureReason) {
    /** 无法判定不等于无漏洞：按未评估处理，整体不闭合。 */
    return {
      ...base,
      affectedBy,
      evaluated: false,
      reason: failureReason,
    };
  }

  return { ...base, affectedBy, evaluated: true };
}

/**
 * 执行闭合扫描。
 * 闭合条件：lockfileVersion 3 可解析，且每个锁定包版本都拿到确定评估结果。
 */
export function scanLockfile(lock: PackageLock, options: ScanOptions): LockfileClosureResult {
  const allowedHosts = options.allowedHosts ?? DEFAULT_ALLOWED_HOSTS;
  const errors: string[] = [];

  if (lock.lockfileVersion !== SUPPORTED_LOCKFILE_VERSION) {
    errors.push(
      `仅支持 lockfileVersion ${SUPPORTED_LOCKFILE_VERSION}，实际为 ${String(lock.lockfileVersion)}`,
    );
  }

  const packages =
    lock.lockfileVersion === SUPPORTED_LOCKFILE_VERSION ? parseLockedPackages(lock) : [];
  const hostViolations = checkAllResolvedHosts(packages, allowedHosts);

  const evaluations = packages.map((pkg) => evaluatePackage(pkg, options.advisories));

  for (const evaluation of evaluations) {
    if (!evaluation.evaluated) {
      errors.push(
        `包 ${evaluation.realName}@${evaluation.version}（${evaluation.installPath}）缺少评估结果：${evaluation.reason ?? 'unknown'}`,
      );
    }
  }

  for (const violation of hostViolations) {
    errors.push(
      `包 ${violation.realName}@${violation.version} 的 resolved 主机违规：${violation.reason}` +
        `${violation.host ? `（host=${violation.host}）` : ''}`,
    );
  }

  const unevaluated = evaluations.some((item) => !item.evaluated);
  const affectedPackages = evaluations.filter((item) => item.affectedBy.length > 0);
  const fullyClosed = lock.lockfileVersion === SUPPORTED_LOCKFILE_VERSION && !unevaluated;
  const passed = fullyClosed && hostViolations.length === 0 && affectedPackages.length === 0;

  return {
    lockfileVersion: lock.lockfileVersion,
    packages,
    evaluations,
    hostViolations,
    closed: fullyClosed,
    passed,
    errors,
  };
}
