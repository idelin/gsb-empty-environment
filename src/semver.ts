/**
 * 最小 SemVer 2.0.0 解析与优先级比较，专门服务于 OSV SEMVER 区间判定。
 * 不引入第三方依赖，构建元数据按规范不参与优先级比较。
 */

export interface SemVer {
  major: number;
  minor: number;
  patch: number;
  /** 预发布标识符；空数组表示正式版，正式版优先级高于同号预发布版。 */
  prerelease: PrereleaseIdentifier[];
}

type PrereleaseIdentifier = string | number;

const SEMVER_RE =
  /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;

/** 解析失败返回 undefined，调用方据此判定评估无法闭合。 */
export function parseSemVer(raw: string): SemVer | undefined {
  if (typeof raw !== 'string') {
    return undefined;
  }
  const match = SEMVER_RE.exec(raw.trim());
  if (!match) {
    return undefined;
  }
  const [, major, minor, patch, prereleaseRaw] = match;
  const prerelease: PrereleaseIdentifier[] = [];
  if (prereleaseRaw !== undefined) {
    for (const part of prereleaseRaw.split('.')) {
      if (/^\d+$/.test(part)) {
        prerelease.push(Number(part));
      } else {
        prerelease.push(part);
      }
    }
  }
  return {
    major: Number(major),
    minor: Number(minor),
    patch: Number(patch),
    prerelease,
  };
}

/**
 * SemVer 优先级比较：
 * - 主/次/修订号按数值比较；
 * - 同号时带预发布的版本小于正式版；
 * - 预发布标识符按数值、ASCII 词法及“数值 < 字母数字”规则逐段比较。
 */
export function compareSemVer(left: SemVer, right: SemVer): -1 | 0 | 1 {
  const core: Array<keyof SemVer> = ['major', 'minor', 'patch'];
  for (const field of core) {
    if (left[field] !== right[field]) {
      return left[field] < right[field] ? -1 : 1;
    }
  }

  if (left.prerelease.length === 0 && right.prerelease.length === 0) {
    return 0;
  }
  /** 有预发布的版本优先级低于无预发布的正式版。 */
  if (left.prerelease.length === 0) {
    return 1;
  }
  if (right.prerelease.length === 0) {
    return -1;
  }

  const max = Math.max(left.prerelease.length, right.prerelease.length);
  for (let i = 0; i < max; i += 1) {
    const leftPart = left.prerelease[i];
    const rightPart = right.prerelease[i];
    if (leftPart === undefined) {
      return -1;
    }
    if (rightPart === undefined) {
      return 1;
    }
    if (typeof leftPart === 'number' && typeof rightPart === 'number') {
      if (leftPart !== rightPart) {
        return leftPart < rightPart ? -1 : 1;
      }
      continue;
    }
    /** 数值标识符优先级低于字母数字标识符。 */
    if (typeof leftPart === 'number') {
      return -1;
    }
    if (typeof rightPart === 'number') {
      return 1;
    }
    if (leftPart !== rightPart) {
      return leftPart < rightPart ? -1 : 1;
    }
  }
  return 0;
}

/** 便捷比较：解析失败时返回 undefined。 */
export function compareVersions(leftRaw: string, rightRaw: string): -1 | 0 | 1 | undefined {
  const left = parseSemVer(leftRaw);
  const right = parseSemVer(rightRaw);
  if (!left || !right) {
    return undefined;
  }
  return compareSemVer(left, right);
}
