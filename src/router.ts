/**
 * 路由编译与匹配。
 *
 * 匹配优先级（针对同一条路径，逐路由取最优展开后再比较）：
 *   1. 字面段更多者优先；
 *   2. 省略的可选组更少者优先；
 *   3. 注册序号小者优先。
 *
 * 编译期冲突检测：若两条路由对某条路径的最优得分（字面数、省略数）相同，
 * 且没有第三条路由在该路径上严格更优，则胜者只能由注册序号决定，属于
 * “同优先级重叠”。通过对两两展开做合一（unification）求出具体见证路径
 * 并报告。该检测是精确的：见证路径的参数位使用与全表字面均不冲突的
 * 占位符，因此各路由在见证上的可匹配展开集与真实冲突路径一致。
 */

import {
  expandPattern,
  parsePattern,
  type Expansion,
  type ParsedPattern,
  type SegmentNode,
} from "./pattern.ts";

export interface Score {
  literals: number;
  omissions: number;
}

export interface CompiledRoute<T> {
  readonly index: number;
  readonly pattern: string;
  readonly handler: T;
  readonly parsed: ParsedPattern;
  readonly expansions: readonly Expansion[];
}

export interface Conflict {
  /** 注册序号较小的路由。 */
  readonly firstIndex: number;
  /** 注册序号较大的路由。 */
  readonly secondIndex: number;
  readonly firstPattern: string;
  readonly secondPattern: string;
  /** 同时命中两条路由且得分相同的具体路径。 */
  readonly witness: string;
  /** 两条路由在见证路径上的共同得分。 */
  readonly score: Score;
}

export class ConflictError extends Error {
  readonly conflicts: readonly Conflict[];
  constructor(conflicts: readonly Conflict[]) {
    const lines = conflicts.map(
      (c) =>
        `  #${c.firstIndex} ${JSON.stringify(c.firstPattern)} vs #${c.secondIndex} ` +
        `${JSON.stringify(c.secondPattern)} — witness ${JSON.stringify(c.witness)} ` +
        `(literals=${c.score.literals}, omissions=${c.score.omissions})`,
    );
    super(`ambiguous routes (same-priority overlap):\n${lines.join("\n")}`);
    this.name = "ConflictError";
    this.conflicts = conflicts;
  }
}

export interface Match<T> {
  readonly handler: T;
  readonly pattern: string;
  readonly index: number;
  readonly params: Record<string, string>;
  readonly score: Score;
}

export interface CompileOptions {
  /** 单条模式展开上限，默认 256。 */
  maxExpansions?: number;
  /** 发现同优先级重叠时："collect"（默认）记录到 conflicts；"throw" 抛 ConflictError。 */
  onConflict?: "collect" | "throw";
}

/** 逐段百分号解码；非法编码保留原文。解码发生在分段之后，%2F 不会重新分段。 */
export function safeDecode(segment: string): string {
  try {
    return decodeURIComponent(segment);
  } catch {
    return segment;
  }
}

/** 去掉 query/hash，按 "/" 分段（忽略空段），再逐段解码。 */
export function splitPath(path: string): string[] {
  let p = path;
  const cut = p.search(/[?#]/);
  if (cut >= 0) p = p.slice(0, cut);
  return p
    .split("/")
    .filter((s) => s.length > 0)
    .map(safeDecode);
}

function matchSegments(
  segments: readonly SegmentNode[],
  path: readonly string[],
): Record<string, string> | null {
  const params: Record<string, string> = {};
  let i = 0;
  for (const seg of segments) {
    if (seg.kind === "splat") {
      const rest = path.slice(i);
      if (rest.length < 1) return null; // splat 至少匹配一段
      params[seg.name] = rest.join("/");
      return params;
    }
    if (i >= path.length) return null;
    const cur = path[i];
    if (seg.kind === "literal") {
      if (cur !== seg.value) return null;
    } else {
      params[seg.name] = cur;
    }
    i++;
  }
  return i === path.length ? params : null;
}

function scoreBetter(a: Score, b: Score): boolean {
  return a.literals !== b.literals
    ? a.literals > b.literals
    : a.omissions < b.omissions;
}

function scoreStrictlyBetter(a: Score, b: Score): boolean {
  return scoreBetter(a, b) && (a.literals !== b.literals || a.omissions !== b.omissions);
}

interface BestHit {
  params: Record<string, string>;
  score: Score;
}

/** 单条路由对一条（已解码的）路径的最优命中；无命中返回 null。 */
function bestHit<T>(route: CompiledRoute<T>, path: readonly string[]): BestHit | null {
  let best: BestHit | null = null;
  for (const exp of route.expansions) {
    const params = matchSegments(exp.segments, path);
    if (params === null) continue;
    const score: Score = { literals: exp.literals, omissions: exp.omissions };
    if (best === null || scoreBetter(score, best.score)) {
      best = { params, score };
    }
  }
  return best;
}

/**
 * 合一两条扁平段序列：若存在同时匹配两者的路径，返回一条具体见证路径
 * （参数位用不与任何字面冲突的占位符填充），否则返回 null。
 */
function unify(
  a: readonly SegmentNode[],
  b: readonly SegmentNode[],
  fresh: () => string,
): string[] | null {
  const out: string[] = [];
  let i = 0;
  let j = 0;
  for (;;) {
    const sa = i < a.length ? a[i] : undefined;
    const sb = j < b.length ? b[j] : undefined;
    if (sa === undefined && sb === undefined) return out;
    if ((sa !== undefined && sa.kind === "splat") || (sb !== undefined && sb.kind === "splat")) {
      const rest = sa !== undefined && sa.kind === "splat" ? b.slice(j) : a.slice(i);
      if (rest.length === 0) return null; // splat 至少需要一段，另一侧已耗尽
      for (const seg of rest) out.push(seg.kind === "literal" ? seg.value : fresh());
      return out;
    }
    if (sa === undefined || sb === undefined) return null; // 长度不一致
    if (sa.kind === "literal" && sb.kind === "literal") {
      if (sa.value !== sb.value) return null;
      out.push(sa.value);
    } else if (sa.kind === "literal") {
      out.push(sa.value);
    } else if (sb.kind === "literal") {
      out.push(sb.value);
    } else {
      out.push(fresh());
    }
    i++;
    j++;
  }
}

function detectConflicts<T>(routes: readonly CompiledRoute<T>[]): Conflict[] {
  const literalValues = new Set<string>();
  for (const r of routes) {
    for (const e of r.expansions) {
      for (const s of e.segments) {
        if (s.kind === "literal") literalValues.add(s.value);
      }
    }
  }
  let counter = 0;
  const fresh = (): string => {
    let token: string;
    do {
      token = `<v${counter++}>`;
    } while (literalValues.has(token));
    return token;
  };

  const conflicts: Conflict[] = [];
  for (let i = 0; i < routes.length; i++) {
    for (let j = i + 1; j < routes.length; j++) {
      const a = routes[i];
      const b = routes[j];
      let found: Conflict | null = null;
      outer: for (const ea of a.expansions) {
        for (const eb of b.expansions) {
          const witness = unify(ea.segments, eb.segments, fresh);
          if (witness === null) continue;
          const ha = bestHit(a, witness);
          const hb = bestHit(b, witness);
          if (
            ha === null ||
            hb === null ||
            ha.score.literals !== hb.score.literals ||
            ha.score.omissions !== hb.score.omissions
          ) {
            continue;
          }
          // 仅当没有第三条路由在该见证上严格更优时，平局才构成真实的二义性。
          let dominated = false;
          for (const other of routes) {
            if (other.index === a.index || other.index === b.index) continue;
            const ho = bestHit(other, witness);
            if (ho !== null && scoreStrictlyBetter(ho.score, ha.score)) {
              dominated = true;
              break;
            }
          }
          if (!dominated) {
            found = {
              firstIndex: a.index,
              secondIndex: b.index,
              firstPattern: a.pattern,
              secondPattern: b.pattern,
              witness: "/" + witness.join("/"),
              score: ha.score,
            };
            break outer; // 每对路由报告一个见证即可
          }
        }
      }
      if (found !== null) conflicts.push(found);
    }
  }
  return conflicts;
}

export class CompiledRouter<T> {
  readonly routes: readonly CompiledRoute<T>[];
  readonly conflicts: readonly Conflict[];

  constructor(routes: readonly CompiledRoute<T>[], conflicts: readonly Conflict[]) {
    this.routes = routes;
    this.conflicts = conflicts;
  }

  /** 命中优先级最高的路由；无命中返回 null。 */
  match(path: string): Match<T> | null {
    const segments = splitPath(path);
    let best: Match<T> | null = null;
    for (const route of this.routes) {
      const hit = bestHit(route, segments);
      if (hit === null) continue;
      if (best === null || scoreBetter(hit.score, best.score)) {
        best = {
          handler: route.handler,
          pattern: route.pattern,
          index: route.index,
          params: hit.params,
          score: hit.score,
        };
      }
    }
    return best;
  }

  /** 全部命中，按优先级（字面数 ↓、省略数 ↑、注册序号 ↑）排序。 */
  matchAll(path: string): Match<T>[] {
    const segments = splitPath(path);
    const out: Match<T>[] = [];
    for (const route of this.routes) {
      const hit = bestHit(route, segments);
      if (hit === null) continue;
      out.push({
        handler: route.handler,
        pattern: route.pattern,
        index: route.index,
        params: hit.params,
        score: hit.score,
      });
    }
    out.sort((x, y) => {
      if (x.score.literals !== y.score.literals) return y.score.literals - x.score.literals;
      if (x.score.omissions !== y.score.omissions) return x.score.omissions - y.score.omissions;
      return x.index - y.index;
    });
    return out;
  }
}

export function compileRouter<T>(
  entries: readonly { pattern: string; handler: T }[],
  options: CompileOptions = {},
): CompiledRouter<T> {
  const routes: CompiledRoute<T>[] = entries.map((entry, index) => {
    const parsed = parsePattern(entry.pattern);
    return {
      index,
      pattern: entry.pattern,
      handler: entry.handler,
      parsed,
      expansions: expandPattern(parsed.nodes, options.maxExpansions),
    };
  });
  const conflicts = detectConflicts(routes);
  if (conflicts.length > 0 && options.onConflict === "throw") {
    throw new ConflictError(conflicts);
  }
  return new CompiledRouter(routes, conflicts);
}

/** 便捷建造器：链式注册后编译。 */
export class Router<T> {
  private readonly entries: { pattern: string; handler: T }[] = [];

  add(pattern: string, handler: T): this {
    this.entries.push({ pattern, handler });
    return this;
  }

  compile(options?: CompileOptions): CompiledRouter<T> {
    return compileRouter(this.entries, options);
  }
}

export function createRouter<T>(): Router<T> {
  return new Router<T>();
}
