import type { PathSegment } from "./types.ts";

export type RuleSegment = string | "*";

export function parseRulePath(path: string): RuleSegment[] {
  const trimmed = path.startsWith("$.") ? path.slice(2) : path === "$" ? "" : path;
  if (trimmed === "") return [];
  const segments: RuleSegment[] = [];
  let current = "";
  let i = 0;
  let justClosedBracket = false;
  const pushCurrent = () => {
    if (current === "") throw new Error(`Invalid rule path: ${path}`);
    segments.push(current);
    current = "";
  };
  while (i < trimmed.length) {
    const ch = trimmed[i] as string;
    if (ch === ".") {
      if (justClosedBracket) {
        justClosedBracket = false;
      } else {
        pushCurrent();
      }
      i += 1;
    } else if (ch === "[") {
      if (current !== "") pushCurrent();
      const close = trimmed.indexOf("]", i);
      if (close === -1) throw new Error(`Invalid rule path: ${path}`);
      const inner = trimmed.slice(i + 1, close);
      if (inner === "*" || inner === "") {
        segments.push("*");
      } else if (/^\d+$/.test(inner)) {
        segments.push(inner);
      } else {
        segments.push(inner);
      }
      i = close + 1;
      justClosedBracket = true;
    } else {
      justClosedBracket = false;
      current += ch;
      i += 1;
    }
  }
  if (current !== "") pushCurrent();
  return segments;
}

export function matchRule(
  ruleSegments: RuleSegment[],
  path: PathSegment[],
): boolean {
  if (ruleSegments.length !== path.length) return false;
  for (let i = 0; i < ruleSegments.length; i += 1) {
    const rule = ruleSegments[i] as RuleSegment;
    if (rule === "*") continue;
    if (rule !== String(path[i])) return false;
  }
  return true;
}

export function formatPath(segments: PathSegment[]): string {
  let out = "$";
  for (const seg of segments) {
    if (typeof seg === "number") {
      out += `[${seg}]`;
    } else if (/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(seg)) {
      out += `.${seg}`;
    } else {
      out += `[${JSON.stringify(seg)}]`;
    }
  }
  return out;
}
