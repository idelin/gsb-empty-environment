export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export class JsonMoveError extends Error {
  readonly path: string;
  constructor(message: string, path: string) {
    super(`${message} (path: ${path})`);
    this.name = "JsonMoveError";
    this.path = path;
  }
}

const hasOwn = (obj: object, key: string): boolean =>
  Object.prototype.hasOwnProperty.call(obj, key);

export function isContainer(
  value: JsonValue,
): value is JsonValue[] | { [key: string]: JsonValue } {
  return value !== null && typeof value === "object";
}

const INDEX_RE = /^(0|[1-9]\d*)$/;
const isIndex = (seg: string): boolean => INDEX_RE.test(seg);

// ---------- path parsing / formatting (\/ and \\ escaping) ----------

export function parsePath(text: string): string[] {
  if (text === "" || text === "/") return [];
  const segs: string[] = [];
  let cur = "";
  let i = text.startsWith("/") ? 1 : 0;
  while (i < text.length) {
    const ch = text[i];
    if (ch === "\\") {
      i++;
      if (i >= text.length) {
        throw new JsonMoveError("dangling escape at end of path", text);
      }
      const next = text[i];
      if (next !== "/" && next !== "\\") {
        throw new JsonMoveError(`invalid escape \\${next}`, text);
      }
      cur += next;
      i++;
    } else if (ch === "/") {
      segs.push(cur);
      cur = "";
      i++;
    } else {
      cur += ch;
      i++;
    }
  }
  segs.push(cur);
  return segs;
}

export function formatPath(segs: string[]): string {
  if (segs.length === 0) return "/";
  return (
    "/" +
    segs.map((s) => s.replace(/\\/g, "\\\\").replace(/\//g, "\\/")).join("/")
  );
}

// ---------- pattern matching ----------

type PatternSeg =
  | { kind: "lit"; value: string }
  | { kind: "one" } //  *  : exactly one segment
  | { kind: "many" }; // ** : zero or more segments

type Capture = string | string[];

function compilePattern(segs: string[]): PatternSeg[] {
  return segs.map((seg) =>
    seg === "*" ? { kind: "one" } : seg === "**" ? { kind: "many" } : { kind: "lit", value: seg },
  );
}

function matchSegments(
  pat: PatternSeg[],
  segs: string[],
  pi: number,
  si: number,
  caps: Capture[],
): boolean {
  if (pi === pat.length) return si === segs.length;
  const p = pat[pi];
  if (p.kind === "lit") {
    return (
      si < segs.length &&
      segs[si] === p.value &&
      matchSegments(pat, segs, pi + 1, si + 1, caps)
    );
  }
  if (p.kind === "one") {
    if (si >= segs.length) return false;
    const mark = caps.length;
    caps.push(segs[si]);
    if (matchSegments(pat, segs, pi + 1, si + 1, caps)) return true;
    caps.length = mark;
    return false;
  }
  // "many": greedy, longest first
  for (let k = segs.length - si; k >= 0; k--) {
    const mark = caps.length;
    caps.push(segs.slice(si, si + k));
    if (matchSegments(pat, segs, pi + 1, si + k, caps)) return true;
    caps.length = mark;
  }
  return false;
}

// ---------- replacement template ----------

type ReplacementSeg =
  | { kind: "lit"; value: string }
  | { kind: "cap"; index: number };

function compileReplacement(text: string, pattern: PatternSeg[]): ReplacementSeg[] {
  const segs = parsePath(text);
  if (segs.length === 0) {
    throw new JsonMoveError("replacement must not be the root path", text);
  }
  const caps = pattern.filter((p) => p.kind !== "lit");
  return segs.map((seg) => {
    if (seg === "*") {
      if (caps.length !== 1 || caps[0].kind !== "one") {
        throw new JsonMoveError(
          '"*" in replacement requires exactly one "*" capture in the pattern; use $N otherwise',
          text,
        );
      }
      return { kind: "cap", index: 0 };
    }
    if (seg === "**") {
      if (caps.length !== 1 || caps[0].kind !== "many") {
        throw new JsonMoveError(
          '"**" in replacement requires exactly one "**" capture in the pattern; use $N otherwise',
          text,
        );
      }
      return { kind: "cap", index: 0 };
    }
    const m = /^\$([1-9]\d*)$/.exec(seg);
    if (m) {
      const index = Number(m[1]) - 1;
      if (index >= caps.length) {
        throw new JsonMoveError(
          `replacement references $${index + 1} but the pattern has only ${caps.length} capture(s)`,
          text,
        );
      }
      return { kind: "cap", index };
    }
    return { kind: "lit", value: seg };
  });
}

function expandReplacement(repl: ReplacementSeg[], caps: Capture[]): string[] {
  const out: string[] = [];
  for (const seg of repl) {
    if (seg.kind === "lit") {
      out.push(seg.value);
    } else {
      const cap = caps[seg.index];
      if (Array.isArray(cap)) out.push(...cap);
      else out.push(cap);
    }
  }
  return out;
}

// ---------- tree operations ----------

function getAt(
  root: JsonValue,
  segs: string[],
): { found: boolean; value?: JsonValue } {
  let node: JsonValue = root;
  for (const seg of segs) {
    if (Array.isArray(node)) {
      if (!isIndex(seg) || Number(seg) >= node.length) return { found: false };
      node = node[Number(seg)];
    } else if (isContainer(node)) {
      if (!hasOwn(node, seg)) return { found: false };
      node = node[seg];
    } else {
      return { found: false };
    }
  }
  return { found: true, value: node };
}

function deleteAt(root: JsonValue, segs: string[]): void {
  let node: JsonValue = root;
  for (let i = 0; i < segs.length - 1; i++) {
    const seg = segs[i];
    if (Array.isArray(node)) node = node[Number(seg)];
    else if (isContainer(node) && hasOwn(node, seg)) node = node[seg];
    else throw new JsonMoveError("internal: source vanished during delete", formatPath(segs));
  }
  const last = segs[segs.length - 1];
  if (Array.isArray(node)) {
    node.splice(Number(last), 1);
  } else if (isContainer(node)) {
    delete node[last];
  }
}

function freshContainer(nextSeg: string): JsonValue {
  return isIndex(nextSeg) ? [] : {};
}

function setAt(root: JsonValue, segs: string[], value: JsonValue): void {
  let node = root as JsonValue[] | { [key: string]: JsonValue };
  for (let i = 0; i < segs.length - 1; i++) {
    const seg = segs[i];
    let next: JsonValue | undefined;
    if (Array.isArray(node)) {
      if (!isIndex(seg) || Number(seg) > node.length) {
        throw new JsonMoveError(`array index ${seg} out of bounds`, formatPath(segs.slice(0, i + 1)));
      }
      next = node[Number(seg)];
      if (next === undefined || !isContainer(next)) {
        next = freshContainer(segs[i + 1]);
        node[Number(seg)] = next;
      }
    } else {
      next = node[seg];
      if (next === undefined || !isContainer(next)) {
        next = freshContainer(segs[i + 1]);
        node[seg] = next;
      }
    }
    node = next as JsonValue[] | { [key: string]: JsonValue };
  }
  const last = segs[segs.length - 1];
  if (Array.isArray(node)) {
    if (!isIndex(last) || Number(last) > node.length) {
      throw new JsonMoveError(`array index ${last} out of bounds`, formatPath(segs));
    }
    node[Number(last)] = value;
  } else {
    node[last] = value;
  }
}

// ---------- structural equality: object keys unordered, arrays ordered ----------

export function deepEqual(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((v, i) => deepEqual(v, b[i]));
  }
  if (isContainer(a) && isContainer(b) && !Array.isArray(a) && !Array.isArray(b)) {
    const ka = Object.keys(a);
    const kb = Object.keys(b);
    return (
      ka.length === kb.length &&
      ka.every((k) => hasOwn(b, k) && deepEqual(a[k], b[k]))
    );
  }
  return false;
}

// ---------- segment helpers ----------

const segsEqual = (a: string[], b: string[]): boolean =>
  a.length === b.length && a.every((s, i) => s === b[i]);

const isPrefix = (prefix: string[], segs: string[]): boolean =>
  prefix.length <= segs.length && prefix.every((s, i) => s === segs[i]);

const isStrictPrefix = (prefix: string[], segs: string[]): boolean =>
  prefix.length < segs.length && isPrefix(prefix, segs);

function compareSegs(a: string[], b: string[]): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (a[i] === b[i]) continue;
    if (isIndex(a[i]) && isIndex(b[i])) return Number(a[i]) - Number(b[i]);
    return a[i] < b[i] ? -1 : 1;
  }
  return a.length - b.length;
}

// ---------- planning (phase 1: match, phase 2: conflict check) ----------

export interface PlanEntry {
  from: string[];
  to: string[];
  value: JsonValue;
  noop: boolean; // target already holds a deep-equal value; nothing to place
}

interface Match {
  path: string[];
  value: JsonValue;
  caps: Capture[];
}

// Pre-order walk; the outermost matching node wins and its subtree is
// carried along, so matched sources are always prefix-free and newly
// created nodes are never re-scanned.
function collectMatches(root: JsonValue, pattern: PatternSeg[]): Match[] {
  const out: Match[] = [];
  const visit = (node: JsonValue, path: string[]): void => {
    if (path.length > 0) {
      const caps: Capture[] = [];
      if (matchSegments(pattern, path, 0, 0, caps)) {
        out.push({ path, value: node, caps });
        return;
      }
    }
    if (Array.isArray(node)) {
      node.forEach((v, i) => visit(v, [...path, String(i)]));
    } else if (isContainer(node)) {
      for (const key of Object.keys(node)) visit(node[key], [...path, key]);
    }
  };
  visit(root, []);
  return out;
}

export function planMoves(
  root: JsonValue,
  patternText: string,
  replacementText: string,
): PlanEntry[] {
  const patternSegs = parsePath(patternText);
  if (patternSegs.length === 0) {
    throw new JsonMoveError("pattern must not be the root path", patternText);
  }
  const pattern = compilePattern(patternSegs);
  const replacement = compileReplacement(replacementText, pattern);

  const entries: PlanEntry[] = [];
  for (const m of collectMatches(root, pattern)) {
    const to = expandReplacement(replacement, m.caps);
    if (segsEqual(m.path, to)) continue; // identity move
    entries.push({ from: m.path, to, value: m.value, noop: false });
  }

  // Two matches mapping to the same target must agree on the value.
  const byTarget = new Map<string, PlanEntry>();
  for (const e of entries) {
    const key = formatPath(e.to);
    const prev = byTarget.get(key);
    if (prev) {
      if (!deepEqual(prev.value, e.value)) {
        throw new JsonMoveError(
          `conflict: ${formatPath(prev.from)} and ${formatPath(e.from)} both move to ${key} with different values`,
          key,
        );
      }
    } else {
      byTarget.set(key, e);
    }
  }

  const sources = entries.map((e) => e.from);
  const vacated = (p: string[]): boolean => sources.some((s) => isPrefix(s, p));

  // Check each target against the original tree.
  for (const e of entries) {
    for (let i = 1; i < e.to.length; i++) {
      const prefix = e.to.slice(0, i);
      if (vacated(prefix)) break; // subtree is removed, then rebuilt
      const r = getAt(root, prefix);
      if (!r.found) break; // will be created
      if (!isContainer(r.value)) {
        throw new JsonMoveError(
          `cannot create ${formatPath(e.to)}: ${formatPath(prefix)} is not a container`,
          formatPath(prefix),
        );
      }
    }
    if (vacated(e.to)) continue;
    const r = getAt(root, e.to);
    if (!r.found) continue;
    if (deepEqual(r.value, e.value)) {
      e.noop = true;
    } else {
      throw new JsonMoveError(
        `conflict: target of ${formatPath(e.from)} already exists with a different value`,
        formatPath(e.to),
      );
    }
  }

  // A non-noop target must not swallow another target below it.
  const unique = [...byTarget.values()];
  for (const outer of unique) {
    if (outer.noop) continue;
    for (const inner of unique) {
      if (inner !== outer && isStrictPrefix(outer.to, inner.to)) {
        throw new JsonMoveError(
          `conflict: target ${formatPath(inner.to)} lies inside target ${formatPath(outer.to)}`,
          formatPath(outer.to),
        );
      }
    }
  }

  return entries;
}

// ---------- apply ----------

export function applyMoves(
  root: JsonValue,
  patternText: string,
  replacementText: string,
): JsonValue {
  const entries = planMoves(root, patternText, replacementText);
  const result = structuredClone(root);

  // 1. Remove every source (deepest / highest array index first).
  const deletions = entries.map((e) => e.from).sort((a, b) => compareSegs(b, a));
  for (const path of deletions) deleteAt(result, path);

  // 2. Place every target (shallowest / lowest array index first).
  const seen = new Set<string>();
  const placements: { to: string[]; value: JsonValue }[] = [];
  for (const e of entries) {
    if (e.noop) continue;
    const key = formatPath(e.to);
    if (seen.has(key)) continue;
    seen.add(key);
    placements.push({ to: e.to, value: e.value });
  }
  placements.sort((a, b) => compareSegs(a.to, b.to));
  for (const p of placements) setAt(result, p.to, p.value);

  return result;
}
