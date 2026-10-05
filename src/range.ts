export type RangeOp = ">=" | "<=" | ">" | "<" | "=";

export interface Comparator {
  op: RangeOp;
  version: number;
}

const TOKEN = /^(>=|<=|==|=|>|<)?(\d+)$/;

/**
 * Parse a range expression over integer versions.
 * Syntax: space/comma separated comparators (ANDed), e.g. ">=2 <5".
 * A bare number means exact equality; "*" or empty means any version.
 */
export function parseRange(src: string | undefined): Comparator[] {
  if (src === undefined) return [];
  const text = src.trim();
  if (text === "" || text === "*") return [];
  return text.split(/[,\s]+/).map((token) => {
    const m = TOKEN.exec(token);
    if (!m) throw new Error(`invalid range token: "${token}" in "${src}"`);
    const raw = m[1] ?? "=";
    const op = (raw === "==" ? "=" : raw) as RangeOp;
    return { op, version: parseInt(m[2], 10) };
  });
}

export function satisfies(version: number, comps: Comparator[]): boolean {
  for (const c of comps) {
    switch (c.op) {
      case ">=": if (!(version >= c.version)) return false; break;
      case "<=": if (!(version <= c.version)) return false; break;
      case ">":  if (!(version > c.version))  return false; break;
      case "<":  if (!(version < c.version))  return false; break;
      case "=":  if (!(version === c.version)) return false; break;
    }
  }
  return true;
}

export function filterVersions(versions: number[], range: string | undefined): number[] {
  const comps = parseRange(range);
  return versions.filter((v) => satisfies(v, comps));
}
