/**
 * 路由模式解析：字面段、命名段（:name）、末尾多段（*name）、可选组 (/...)。
 *
 * 语法（段对齐，组只能包整段）：
 *   pattern := "/" seq
 *   seq     := item ("/" item)*
 *   item    := literal | ":name" | "*name" | "(/" seq ")"
 * 约束：
 *   - "*name"（splat）只能出现在顶层最后一段，且至少匹配 1 段；
 *   - 可选组不可为空、可嵌套，但组内不允许 splat；
 *   - 同一模式内参数名（含 splat）不得重复。
 */

export type SegmentNode =
  | { kind: "literal"; value: string }
  | { kind: "param"; name: string }
  | { kind: "splat"; name: string };

export interface GroupNode {
  kind: "group";
  children: PatternNode[];
}

export type PatternNode = SegmentNode | GroupNode;

export interface ParsedPattern {
  source: string;
  nodes: PatternNode[];
  /** 命名段与 splat 名，按出现顺序。 */
  paramNames: string[];
}

export class PatternError extends Error {
  readonly source: string;
  readonly offset: number;
  constructor(message: string, source: string, offset: number) {
    super(`${message} (pattern ${JSON.stringify(source)}, offset ${offset})`);
    this.name = "PatternError";
    this.source = source;
    this.offset = offset;
  }
}

const NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

class Parser {
  pos = 0;
  private readonly src: string;
  constructor(src: string) {
    this.src = src;
  }

  atEnd(): boolean {
    return this.pos >= this.src.length;
  }

  private peek(): string {
    return this.src[this.pos];
  }

  private skipSlashes(): void {
    while (!this.atEnd() && this.peek() === "/") this.pos++;
  }

  parseSequence(depth: number): PatternNode[] {
    const nodes: PatternNode[] = [];
    for (;;) {
      this.skipSlashes();
      if (this.atEnd()) {
        if (depth > 0) throw new PatternError("unclosed '('", this.src, this.pos);
        return nodes;
      }
      const ch = this.peek();
      if (ch === ")") {
        if (depth === 0) throw new PatternError("unmatched ')'", this.src, this.pos);
        return nodes; // 调用方消费 ')'
      }
      if (ch === "(") {
        const openAt = this.pos;
        this.pos++;
        if (this.atEnd() || this.peek() !== "/") {
          throw new PatternError(
            "optional group must start with '(/' and contain whole segments",
            this.src,
            openAt,
          );
        }
        const children = this.parseSequence(depth + 1);
        if (children.length === 0) {
          throw new PatternError("optional group must not be empty", this.src, openAt);
        }
        this.pos++; // 消费 ')'
        nodes.push({ kind: "group", children });
        continue;
      }
      nodes.push(this.parseSegment(depth));
    }
  }

  private parseSegment(depth: number): SegmentNode {
    const start = this.pos;
    while (!this.atEnd() && this.peek() !== "/" && this.peek() !== "(" && this.peek() !== ")") {
      this.pos++;
    }
    const text = this.src.slice(start, this.pos);
    if (text.startsWith(":")) {
      const name = text.slice(1);
      if (!NAME_RE.test(name)) {
        throw new PatternError(`invalid parameter name ${JSON.stringify(name)}`, this.src, start);
      }
      return { kind: "param", name };
    }
    if (text.startsWith("*")) {
      const name = text.slice(1);
      if (!NAME_RE.test(name)) {
        throw new PatternError(`invalid splat name ${JSON.stringify(name)}`, this.src, start);
      }
      if (depth > 0) {
        throw new PatternError("splat '*' is not allowed inside an optional group", this.src, start);
      }
      const save = this.pos;
      this.skipSlashes();
      const last = this.atEnd();
      this.pos = save;
      if (!last) {
        throw new PatternError("splat '*' must be the last segment of the pattern", this.src, start);
      }
      return { kind: "splat", name };
    }
    return { kind: "literal", value: text };
  }
}

export function parsePattern(source: string): ParsedPattern {
  if (typeof source !== "string" || source.length === 0) {
    throw new PatternError("pattern must be a non-empty string", String(source), 0);
  }
  if (!source.startsWith("/")) {
    throw new PatternError("pattern must start with '/'", source, 0);
  }
  const nodes = new Parser(source).parseSequence(0);

  const paramNames: string[] = [];
  const seen = new Set<string>();
  const walk = (ns: PatternNode[]): void => {
    for (const n of ns) {
      if (n.kind === "group") {
        walk(n.children);
      } else if (n.kind !== "literal") {
        if (seen.has(n.name)) {
          throw new PatternError(`duplicate parameter name ${JSON.stringify(n.name)}`, source, 0);
        }
        seen.add(n.name);
        paramNames.push(n.name);
      }
    }
  };
  walk(nodes);

  return { source, nodes, paramNames };
}

/** 展开后的扁平段序列：可选组已取舍，记录字面数与省略数。 */
export interface Expansion {
  segments: SegmentNode[];
  /** 字面段数量（优先级第一关键字，越多越好）。 */
  literals: number;
  /** 被省略的可选组数量（优先级第二关键字，越少越好）。 */
  omissions: number;
}

const DEFAULT_MAX_EXPANSIONS = 256;

/**
 * 把模式展开为全部扁平形态。每个可选组有“省略 / 包含”两种选择，
 * 嵌套组按笛卡尔积展开；超过 maxExpansions 抛错以防组合爆炸。
 */
export function expandPattern(
  nodes: PatternNode[],
  maxExpansions: number = DEFAULT_MAX_EXPANSIONS,
): Expansion[] {
  let acc: Expansion[] = [{ segments: [], literals: 0, omissions: 0 }];
  for (const node of nodes) {
    if (node.kind === "group") {
      const sub = expandPattern(node.children, maxExpansions);
      const next: Expansion[] = [];
      for (const base of acc) {
        next.push({
          segments: base.segments,
          literals: base.literals,
          omissions: base.omissions + 1,
        });
        for (const s of sub) {
          next.push({
            segments: [...base.segments, ...s.segments],
            literals: base.literals + s.literals,
            omissions: base.omissions + s.omissions,
          });
        }
      }
      acc = next;
    } else {
      acc = acc.map((e) => ({
        segments: [...e.segments, node],
        literals: e.literals + (node.kind === "literal" ? 1 : 0),
        omissions: e.omissions,
      }));
    }
    if (acc.length > maxExpansions) {
      throw new PatternError(
        `pattern expands to more than ${maxExpansions} variants (too many optional groups)`,
        "",
        0,
      );
    }
  }
  return acc;
}
