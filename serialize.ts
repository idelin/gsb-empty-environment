import { isTagged, type Tag } from "./tagged.ts";

export interface Violation {
  path: string;
  tags: Tag[];
}

export class ForbiddenTagError extends Error {
  readonly violations: Violation[];

  constructor(violations: Violation[]) {
    super(
      "forbidden tag(s) found: " +
        violations.map((v) => `${v.path} [${v.tags.join(", ")}]`).join("; "),
    );
    this.name = "ForbiddenTagError";
    this.violations = violations;
  }
}

export class CycleError extends Error {
  readonly path: string;

  constructor(path: string) {
    super(`cyclic structure detected at ${path}`);
    this.name = "CycleError";
    this.path = path;
  }
}

export interface SerializeOptions {
  forbid?: Iterable<Tag>;
  space?: string | number;
}

export type Sink = { write(chunk: string): void } | ((chunk: string) => void);

function childPath(parent: string, key: string): string {
  return /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(key)
    ? `${parent}.${key}`
    : `${parent}[${JSON.stringify(key)}]`;
}

interface Inspection {
  violations: Violation[];
  cyclePath: string | null;
}

function inspect(root: unknown, forbid: ReadonlySet<Tag>): Inspection {
  const violations: Violation[] = [];
  let cyclePath: string | null = null;
  const state = new Map<object, "open" | "done">();

  const visit = (node: unknown, path: string): void => {
    let raw = node;
    if (isTagged(node)) {
      const bad = [...node.tags].filter((t) => forbid.has(t));
      if (bad.length > 0) violations.push({ path, tags: bad });
      raw = node.value;
    }
    if (raw === null || typeof raw !== "object") return;
    const seen = state.get(raw);
    if (seen === "open") {
      if (cyclePath === null) cyclePath = path;
      return;
    }
    if (seen === "done") return;
    state.set(raw, "open");
    if (Array.isArray(raw)) {
      for (let i = 0; i < raw.length; i++) {
        visit(raw[i], `${path}[${i}]`);
      }
    } else {
      for (const key of Object.keys(raw)) {
        visit((raw as Record<string, unknown>)[key], childPath(path, key));
      }
    }
    state.set(raw, "done");
  };

  visit(root, "$");
  return { violations, cyclePath };
}

function isPlainObject(value: object): boolean {
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

function toPlain(node: unknown, memo: Map<object, unknown>): unknown {
  if (isTagged(node)) return toPlain(node.value, memo);
  if (node === null || typeof node !== "object") return node;
  if (memo.has(node)) return memo.get(node);
  if (Array.isArray(node)) {
    const out: unknown[] = [];
    memo.set(node, out);
    for (const item of node) out.push(toPlain(item, memo));
    return out;
  }
  if (isPlainObject(node)) {
    const out: Record<string, unknown> = {};
    memo.set(node, out);
    for (const key of Object.keys(node)) {
      out[key] = toPlain((node as Record<string, unknown>)[key], memo);
    }
    return out;
  }
  return node;
}

export function serializeJson(value: unknown, options: SerializeOptions = {}): string {
  const forbid = new Set(options.forbid ?? []);
  const { violations, cyclePath } = inspect(value, forbid);
  if (violations.length > 0) throw new ForbiddenTagError(violations);
  if (cyclePath !== null) throw new CycleError(cyclePath);
  return JSON.stringify(toPlain(value, new Map()), null, options.space);
}

export function writeJson(
  value: unknown,
  sink: Sink,
  options: SerializeOptions = {},
): void {
  const text = serializeJson(value, options);
  if (typeof sink === "function") {
    sink(text);
  } else {
    sink.write(text);
  }
}
