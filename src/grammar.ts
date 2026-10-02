/**
 * 文法表示、文法文本解析、First/Follow 集与最短终结符串（minYield）计算。
 */

export const EPSILON = "ε";
export const END = "$";

export interface Production {
  id: number;
  lhs: string;
  /** 右部符号序列；空数组表示 ε 产生式 */
  rhs: string[];
}

export interface Grammar {
  /** 开始符号（未增广） */
  start: string;
  productions: Production[];
  nonterminals: Set<string>;
  terminals: Set<string>;
  /** lhs -> 该非终结符的所有产生式 */
  byLhs: Map<string, Production[]>;
}

/**
 * 解析 BNF 风格文法文本，例如：
 *   S -> if E then S else S | if E then S | other
 *   E -> cond
 * 规则：
 *  - 每行一条规则，`->` 或 `→` 分隔左右部，`|` 分隔候选式；
 *  - 符号以空白分隔；`ε` / `epsilon` / `EPSILON` 表示空串；
 *  - `#` 或 `//` 之后为注释；
 *  - 第一条规则的左部为开始符号；
 *  - 未出现在任何左部的符号视为终结符。
 */
export function parseGrammar(source: string): Grammar {
  const rules: { lhs: string; rhs: string[] }[] = [];
  for (const rawLine of source.split(/\r?\n/)) {
    const line = rawLine.replace(/(#|\/\/).*$/, "").trim();
    if (!line) continue;
    const m = line.match(/^(\S+)\s*(?:->|→|::=)\s*(.*)$/);
    if (!m) throw new Error(`无法解析的文法行: ${rawLine}`);
    const lhs = m[1];
    const alternatives = m[2].split("|");
    for (const alt of alternatives) {
      const symbols = alt
        .trim()
        .split(/\s+/)
        .filter((s) => s.length > 0)
        .filter((s) => s !== EPSILON && s.toLowerCase() !== "epsilon");
      rules.push({ lhs, rhs: symbols });
    }
  }
  if (rules.length === 0) throw new Error("文法为空");

  const lhsSet = new Set(rules.map((r) => r.lhs));
  const used = new Set<string>();
  for (const r of rules) for (const s of r.rhs) used.add(s);
  const terminals = new Set<string>();
  for (const s of used) if (!lhsSet.has(s)) terminals.add(s);

  const productions: Production[] = rules.map((r, i) => ({ id: i, lhs: r.lhs, rhs: r.rhs }));
  const byLhs = new Map<string, Production[]>();
  for (const p of productions) {
    const list = byLhs.get(p.lhs);
    if (list) list.push(p);
    else byLhs.set(p.lhs, [p]);
  }
  return {
    start: rules[0].lhs,
    productions,
    nonterminals: lhsSet,
    terminals,
    byLhs,
  };
}

/** 返回增广文法：新增产生式 S' -> S（id 为 0），开始符号为 S'。 */
export function augmentGrammar(g: Grammar): Grammar {
  let newStart = g.start + "'";
  while (g.nonterminals.has(newStart) || g.terminals.has(newStart)) newStart += "'";
  const augProductions: Production[] = [{ id: 0, lhs: newStart, rhs: [g.start] }];
  for (const p of g.productions) {
    augProductions.push({ id: p.id + 1, lhs: p.lhs, rhs: p.rhs });
  }
  const byLhs = new Map<string, Production[]>();
  for (const p of augProductions) {
    const list = byLhs.get(p.lhs);
    if (list) list.push(p);
    else byLhs.set(p.lhs, [p]);
  }
  const nonterminals = new Set(g.nonterminals);
  nonterminals.add(newStart);
  return {
    start: newStart,
    productions: augProductions,
    nonterminals,
    terminals: new Set(g.terminals),
    byLhs,
  };
}

/** 计算所有符号的 First 集（含终结符与 $，终结符的 First 为自身）。 */
export function computeFirst(g: Grammar): Map<string, Set<string>> {
  const first = new Map<string, Set<string>>();
  for (const t of g.terminals) first.set(t, new Set([t]));
  first.set(END, new Set([END]));
  for (const nt of g.nonterminals) first.set(nt, new Set());

  let changed = true;
  while (changed) {
    changed = false;
    for (const p of g.productions) {
      const target = first.get(p.lhs)!;
      if (p.rhs.length === 0) {
        if (!target.has(EPSILON)) {
          target.add(EPSILON);
          changed = true;
        }
        continue;
      }
      let nullablePrefix = true;
      for (const sym of p.rhs) {
        for (const s of first.get(sym)!) {
          if (s !== EPSILON && !target.has(s)) {
            target.add(s);
            changed = true;
          }
        }
        if (!first.get(sym)!.has(EPSILON)) {
          nullablePrefix = false;
          break;
        }
      }
      if (nullablePrefix && !target.has(EPSILON)) {
        target.add(EPSILON);
        changed = true;
      }
    }
  }
  return first;
}

/**
 * 计算符号序列的 First 集。
 * 若整个序列可推空，结果中包含 ε。
 */
export function firstOfSequence(
  seq: string[],
  first: Map<string, Set<string>>,
): Set<string> {
  const out = new Set<string>();
  let nullablePrefix = true;
  for (const sym of seq) {
    const f = first.get(sym);
    if (!f) throw new Error(`未知符号: ${sym}`);
    for (const s of f) if (s !== EPSILON) out.add(s);
    if (!f.has(EPSILON)) {
      nullablePrefix = false;
      break;
    }
  }
  if (nullablePrefix) out.add(EPSILON);
  return out;
}

/** 计算所有非终结符的 Follow 集。 */
export function computeFollow(
  g: Grammar,
  first: Map<string, Set<string>>,
): Map<string, Set<string>> {
  const follow = new Map<string, Set<string>>();
  for (const nt of g.nonterminals) follow.set(nt, new Set());
  follow.get(g.start)!.add(END);

  let changed = true;
  while (changed) {
    changed = false;
    for (const p of g.productions) {
      for (let i = 0; i < p.rhs.length; i++) {
        const sym = p.rhs[i];
        if (!g.nonterminals.has(sym)) continue;
        const target = follow.get(sym)!;
        const rest = p.rhs.slice(i + 1);
        const firstRest = firstOfSequence(rest, first);
        for (const s of firstRest) {
          if (s !== EPSILON && !target.has(s)) {
            target.add(s);
            changed = true;
          }
        }
        if (rest.length === 0 || firstRest.has(EPSILON)) {
          for (const s of follow.get(p.lhs)!) {
            if (!target.has(s)) {
              target.add(s);
              changed = true;
            }
          }
        }
      }
    }
  }
  return follow;
}

/**
 * 计算每个符号可推导出的最短终结符串（minYield）。
 * 用于生成冲突的最短输入证据：自动机边上标注的非终结符，
 * 需要消耗一段输入才能被归约出来，最短消耗即 minYield。
 * 若某非终结符无法推出终结符串，则不出现在结果中。
 */
export function computeMinYield(g: Grammar): Map<string, string[]> {
  const best = new Map<string, string[]>();
  for (const t of g.terminals) best.set(t, [t]);

  let changed = true;
  while (changed) {
    changed = false;
    for (const p of g.productions) {
      let total = 0;
      const parts: string[] = [];
      let ok = true;
      for (const sym of p.rhs) {
        const y = best.get(sym);
        if (!y) {
          ok = false;
          break;
        }
        total += y.length;
        parts.push(...y);
      }
      if (!ok) continue;
      const cur = best.get(p.lhs);
      if (!cur || total < cur.length) {
        best.set(p.lhs, parts);
        changed = true;
      }
    }
  }
  return best;
}

/** 产生式的可读形式，如 `S -> if E then S`（ε 产生式显示为 `A -> ε`）。 */
export function formatProduction(g: Grammar, prodId: number): string {
  const p = g.productions[prodId];
  return `${p.lhs} -> ${p.rhs.length ? p.rhs.join(" ") : EPSILON}`;
}
