export type Segment =
  | { kind: "lit"; text: string }
  | { kind: "param"; name: string }
  | { kind: "wild"; name: string };

export type Element =
  | { kind: "seg"; seg: Segment }
  | { kind: "group"; children: Element[] };

export interface Pattern {
  source: string;
  elements: Element[];
}

export interface Variant {
  segments: Segment[];
  literalCount: number;
  omittedCount: number;
}

export class PatternError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PatternError";
  }
}

const NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const MAX_VARIANTS = 1024;

export function parsePattern(source: string): Pattern {
  let pos = 0;

  const fail = (message: string): never => {
    throw new PatternError(`pattern ${JSON.stringify(source)}: ${message} (offset ${pos})`);
  };

  function parseSegment(): Segment {
    const start = pos;
    while (pos < source.length && source[pos] !== "/" && source[pos] !== "(" && source[pos] !== ")") {
      pos++;
    }
    const text = source.slice(start, pos);
    if (text === "") fail("empty segment");
    if (text.startsWith(":")) {
      const name = text.slice(1);
      if (!NAME_RE.test(name)) fail(`invalid parameter name ${JSON.stringify(text)}`);
      return { kind: "param", name };
    }
    if (text.startsWith("*")) {
      const name = text.slice(1);
      if (!NAME_RE.test(name)) fail(`invalid wildcard name ${JSON.stringify(text)}`);
      return { kind: "wild", name };
    }
    return { kind: "lit", text };
  }

  function parseElements(depth: number): Element[] {
    const elements: Element[] = [];
    for (;;) {
      if (pos >= source.length) {
        if (depth > 0) fail("unterminated group, missing ')'");
        return elements;
      }
      const ch = source[pos];
      if (ch === ")") {
        if (depth === 0) fail("unmatched ')'");
        return elements;
      }
      if (ch === "/") {
        pos++;
        elements.push({ kind: "seg", seg: parseSegment() });
        continue;
      }
      if (ch === "(") {
        pos++;
        const children = parseElements(depth + 1);
        pos++; // consume ')'
        if (children.length === 0) fail("empty group");
        elements.push({ kind: "group", children });
        continue;
      }
      fail(depth === 0 ? "expected '/' or '('" : "group contents must start with '/'");
    }
  }

  if (!source.startsWith("/") && !source.startsWith("(")) {
    throw new PatternError(`pattern ${JSON.stringify(source)}: must start with '/' or '('`);
  }
  if (source === "/") {
    return { source, elements: [] };
  }
  const elements = parseElements(0);

  const names = new Set<string>();
  const topLevel: Segment[] = [];
  const visit = (els: Element[], inGroup: boolean): void => {
    for (const el of els) {
      if (el.kind === "group") {
        visit(el.children, true);
        continue;
      }
      const seg = el.seg;
      if (seg.kind === "wild" && inGroup) {
        throw new PatternError(
          `pattern ${JSON.stringify(source)}: wildcard '*${seg.name}' must be a top-level trailing segment`,
        );
      }
      if (seg.kind !== "lit") {
        if (names.has(seg.name)) {
          throw new PatternError(`pattern ${JSON.stringify(source)}: duplicate name '${seg.name}'`);
        }
        names.add(seg.name);
      }
      if (!inGroup) topLevel.push(seg);
    }
  };
  visit(elements, false);

  const wildIndex = topLevel.findIndex((seg) => seg.kind === "wild");
  if (wildIndex !== -1 && wildIndex !== topLevel.length - 1) {
    throw new PatternError(`pattern ${JSON.stringify(source)}: wildcard must be the last segment`);
  }

  return { source, elements };
}

export function expand(pattern: Pattern): Variant[] {
  return expandElements(pattern.elements).map((combo) => ({
    segments: combo.segs,
    literalCount: combo.segs.reduce((n, seg) => n + (seg.kind === "lit" ? 1 : 0), 0),
    omittedCount: combo.omitted,
  }));
}

function expandElements(elements: Element[]): { segs: Segment[]; omitted: number }[] {
  let acc: { segs: Segment[]; omitted: number }[] = [{ segs: [], omitted: 0 }];
  for (const el of elements) {
    if (el.kind === "seg") {
      for (const combo of acc) combo.segs.push(el.seg);
      continue;
    }
    const inner = expandElements(el.children);
    const included = acc.flatMap((combo) =>
      inner.map((sub) => ({
        segs: [...combo.segs, ...sub.segs],
        omitted: combo.omitted + sub.omitted,
      })),
    );
    const excluded = acc.map((combo) => ({ segs: combo.segs, omitted: combo.omitted + 1 }));
    acc = [...included, ...excluded];
    if (acc.length > MAX_VARIANTS) {
      throw new PatternError("too many optional groups (variant explosion)");
    }
  }
  return acc;
}
