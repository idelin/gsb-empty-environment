export type Formula =
  | { kind: "leaf"; node: number }
  | { kind: "threshold"; k: number; children: Formula[] };

export const MASK_A = 1;
export const MASK_B = 2;
export const MASK_N = 4;
export const MASK_ALL = MASK_A | MASK_B | MASK_N;

export const SAT = 1;
export const VIOL = 2;
export const UNKNOWN = 3;
export type Status = typeof SAT | typeof VIOL | typeof UNKNOWN;

export function parseFormula(raw: unknown, nodeCount: number, path = "$"): Formula {
  if (typeof raw === "number") {
    if (!Number.isInteger(raw) || raw < 0 || raw >= nodeCount) {
      throw new Error(`${path}: 节点编号必须是 [0, ${nodeCount}) 内的整数，收到 ${raw}`);
    }
    return { kind: "leaf", node: raw };
  }
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    throw new Error(`${path}: 无法识别的公式描述`);
  }
  const obj = raw as Record<string, unknown>;
  const parseChildren = (list: unknown, p: string): Formula[] => {
    if (!Array.isArray(list) || list.length === 0) {
      throw new Error(`${p}: 子项必须是非空数组`);
    }
    return list.map((c, i) => parseFormula(c, nodeCount, `${p}[${i}]`));
  };
  const makeThreshold = (k: unknown, children: Formula[], p: string): Formula => {
    if (typeof k !== "number" || !Number.isInteger(k) || k < 1 || k > children.length) {
      throw new Error(`${p}: k 必须是 [1, ${children.length}] 内的整数，收到 ${k}`);
    }
    return { kind: "threshold", k, children };
  };
  if ("k" in obj && "of" in obj) {
    const children = parseChildren(obj.of, `${path}.of`);
    return makeThreshold(obj.k, children, path);
  }
  if ("atLeast" in obj && "group" in obj) {
    const children = parseChildren(obj.group, `${path}.group`);
    return makeThreshold(obj.atLeast, children, path);
  }
  if ("any" in obj) {
    return { kind: "threshold", k: 1, children: parseChildren(obj.any, `${path}.any`) };
  }
  if ("all" in obj) {
    const children = parseChildren(obj.all, `${path}.all`);
    return { kind: "threshold", k: children.length, children };
  }
  throw new Error(`${path}: 无法识别的公式描述`);
}

export function evalStatus(
  f: Formula,
  side: typeof MASK_A | typeof MASK_B,
  masks: number[],
): Status {
  if (f.kind === "leaf") {
    const m = masks[f.node];
    if (side === MASK_A) {
      if (m === MASK_A) return SAT;
      if ((m & MASK_A) === 0) return VIOL;
      return UNKNOWN;
    }
    if (m === MASK_B) return SAT;
    if ((m & MASK_B) === 0) return VIOL;
    return UNKNOWN;
  }
  let sat = 0;
  let viol = 0;
  for (const c of f.children) {
    const s = evalStatus(c, side, masks);
    if (s === SAT) sat++;
    else if (s === VIOL) viol++;
  }
  if (sat >= f.k) return SAT;
  if (f.children.length - viol < f.k) return VIOL;
  return UNKNOWN;
}

export function evalFormula(f: Formula, membership: boolean[]): boolean {
  if (f.kind === "leaf") return membership[f.node];
  let count = 0;
  for (const c of f.children) if (evalFormula(c, membership)) count++;
  return count >= f.k;
}

export function appendCounts(
  f: Formula,
  side: typeof MASK_A | typeof MASK_B,
  masks: number[],
  out: number[],
): void {
  if (f.kind === "leaf") return;
  let sat = 0;
  let viol = 0;
  for (const c of f.children) {
    const s = evalStatus(c, side, masks);
    if (s === SAT) sat++;
    else if (s === VIOL) viol++;
  }
  out.push(sat, viol);
  for (const c of f.children) appendCounts(c, side, masks, out);
}
