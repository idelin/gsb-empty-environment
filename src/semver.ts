export interface SemVer {
  major: number;
  minor: number;
  patch: number;
}

export function parseVersion(version: string): SemVer {
  const match = /^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?(?:[-+][0-9A-Za-z.-]+)?$/.exec(version.trim());
  if (!match) throw new Error(`非法版本号: ${version}`);
  return {
    major: Number(match[1]),
    minor: match[2] === undefined ? 0 : Number(match[2]),
    patch: match[3] === undefined ? 0 : Number(match[3]),
  };
}

export function compareVersions(a: SemVer, b: SemVer): number {
  return a.major - b.major || a.minor - b.minor || a.patch - b.patch;
}

export function satisfies(version: string, range: string): boolean {
  const parsed = parseVersion(version);
  const trimmed = range.trim();
  if (trimmed === "" || trimmed === "*") return true;
  return trimmed
    .split("||")
    .some((group) =>
      group
        .trim()
        .split(/\s+/)
        .filter(Boolean)
        .every((part) => satisfiesComparator(parsed, part)),
    );
}

function satisfiesComparator(version: SemVer, comparator: string): boolean {
  if (comparator === "*" || comparator === "") return true;
  const match = /^(\^|~|>=|<=|>|<|=)?(.*)$/.exec(comparator.trim());
  const op = match?.[1] ?? "=";
  const ref = parseVersion(match?.[2] ?? "");
  switch (op) {
    case "=":
      return compareVersions(version, ref) === 0;
    case ">":
      return compareVersions(version, ref) > 0;
    case "<":
      return compareVersions(version, ref) < 0;
    case ">=":
      return compareVersions(version, ref) >= 0;
    case "<=":
      return compareVersions(version, ref) <= 0;
    case "~": {
      const upper: SemVer = { major: ref.major, minor: ref.minor + 1, patch: 0 };
      return compareVersions(version, ref) >= 0 && compareVersions(version, upper) < 0;
    }
    case "^": {
      let upper: SemVer;
      if (ref.major > 0) upper = { major: ref.major + 1, minor: 0, patch: 0 };
      else if (ref.minor > 0) upper = { major: 0, minor: ref.minor + 1, patch: 0 };
      else upper = { major: 0, minor: 0, patch: ref.patch + 1 };
      return compareVersions(version, ref) >= 0 && compareVersions(version, upper) < 0;
    }
    default:
      return false;
  }
}
