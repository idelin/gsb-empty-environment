export interface Semver {
  major: number;
  minor: number;
  patch: number;
  prerelease: string[];
}

const VERSION_RE =
  /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;

export function parseVersion(input: string): Semver {
  const match = VERSION_RE.exec(input.trim());
  if (!match) throw new Error(`非法版本号: ${input}`);
  return {
    major: Number(match[1]),
    minor: Number(match[2]),
    patch: Number(match[3]),
    prerelease: match[4] ? match[4].split('.') : [],
  };
}

function comparePrerelease(a: string[], b: string[]): number {
  if (a.length === 0 && b.length === 0) return 0;
  if (a.length === 0) return 1;
  if (b.length === 0) return -1;
  for (let i = 0; i < Math.min(a.length, b.length); i++) {
    const x = a[i];
    const y = b[i];
    const nx = /^\d+$/.test(x) ? Number(x) : NaN;
    const ny = /^\d+$/.test(y) ? Number(y) : NaN;
    if (!Number.isNaN(nx) && !Number.isNaN(ny)) {
      if (nx !== ny) return nx - ny;
    } else if (Number.isNaN(nx) !== Number.isNaN(ny)) {
      return Number.isNaN(nx) ? 1 : -1;
    } else if (x !== y) {
      return x < y ? -1 : 1;
    }
  }
  return a.length - b.length;
}

export function compareVersions(a: string | Semver, b: string | Semver): number {
  const va = typeof a === 'string' ? parseVersion(a) : a;
  const vb = typeof b === 'string' ? parseVersion(b) : b;
  if (va.major !== vb.major) return va.major - vb.major;
  if (va.minor !== vb.minor) return va.minor - vb.minor;
  if (va.patch !== vb.patch) return va.patch - vb.patch;
  return comparePrerelease(va.prerelease, vb.prerelease);
}

interface PartialVersion {
  major: number | null;
  minor: number | null;
  patch: number | null;
}

function parsePartial(token: string): PartialVersion | null {
  const match = /^v?(\d+|[xX*])(?:\.(\d+|[xX*]))?(?:\.(\d+|[xX*]))?$/.exec(token.trim());
  if (!match) return null;
  const part = (s: string | undefined): number | null =>
    s === undefined || /^[xX*]$/.test(s) ? null : Number(s);
  return { major: part(match[1]), minor: part(match[2]), patch: part(match[3]) };
}

function matchPartial(version: Semver, partial: PartialVersion): boolean {
  if (partial.major !== null && version.major !== partial.major) return false;
  if (partial.minor !== null && version.minor !== partial.minor) return false;
  if (partial.patch !== null && version.patch !== partial.patch) return false;
  return true;
}

function matchCaret(version: Semver, partial: PartialVersion | null): boolean {
  if (!partial || partial.major === null) return false;
  const lower: Semver = {
    major: partial.major,
    minor: partial.minor ?? 0,
    patch: partial.patch ?? 0,
    prerelease: [],
  };
  if (compareVersions(version, lower) < 0) return false;
  if (partial.major > 0) return version.major === partial.major;
  if (partial.minor === null) return version.major === 0;
  if (partial.minor > 0) return version.major === 0 && version.minor === partial.minor;
  if (partial.patch === null) return version.major === 0 && version.minor === 0;
  return version.major === 0 && version.minor === 0 && version.patch === partial.patch;
}

function matchTilde(version: Semver, partial: PartialVersion | null): boolean {
  if (!partial || partial.major === null) return true;
  if (version.major !== partial.major) return false;
  if (partial.minor === null) return true;
  if (version.minor !== partial.minor) return false;
  return version.patch >= (partial.patch ?? 0);
}

function matchOperator(version: Semver, op: string, partial: PartialVersion): boolean {
  if (op === '=') {
    if (partial.major === null) return true;
    if (partial.minor === null || partial.patch === null) return matchPartial(version, partial);
    return (
      compareVersions(version, {
        major: partial.major,
        minor: partial.minor,
        patch: partial.patch,
        prerelease: [],
      }) === 0
    );
  }
  const filled: Semver = {
    major: partial.major ?? 0,
    minor: partial.minor ?? 0,
    patch: partial.patch ?? 0,
    prerelease: [],
  };
  const cmp = compareVersions(version, filled);
  if (op === '>=') return cmp >= 0;
  if (op === '<') return cmp < 0;
  if (op === '>') {
    if (partial.patch !== null) return cmp > 0;
    if (partial.minor !== null)
      return (
        compareVersions(version, {
          major: filled.major,
          minor: filled.minor + 1,
          patch: 0,
          prerelease: [],
        }) >= 0
      );
    return version.major > filled.major;
  }
  // op === '<='
  if (partial.patch !== null) return cmp <= 0;
  if (partial.minor !== null)
    return (
      compareVersions(version, {
        major: filled.major,
        minor: filled.minor + 1,
        patch: 0,
        prerelease: [],
      }) < 0
    );
  return version.major <= filled.major;
}

function matchToken(version: Semver, token: string): boolean {
  if (token === '' || token === '*' || /^[xX]$/.test(token)) return true;
  if (token.startsWith('^')) return matchCaret(version, parsePartial(token.slice(1)));
  if (token.startsWith('~')) return matchTilde(version, parsePartial(token.slice(1)));
  const op = /^([<>]=?|=)\s*(.+)$/.exec(token);
  if (op) {
    const partial = parsePartial(op[2]);
    return partial !== null && matchOperator(version, op[1], partial);
  }
  const partial = parsePartial(token);
  return partial !== null && matchPartial(version, partial);
}

/** 支持 `*`、`1.x`、`1.2.x`、`^`、`~`、`>=` 等比较符、空格 AND 与 `||` OR */
export function satisfies(version: string, range: string): boolean {
  const parsed = parseVersion(version);
  const trimmed = range.trim();
  if (trimmed === '' || trimmed === '*') return true;
  return trimmed.split('||').some((alternative) =>
    alternative
      .trim()
      .split(/\s+/)
      .filter((token) => token.length > 0)
      .every((token) => matchToken(parsed, token)),
  );
}
