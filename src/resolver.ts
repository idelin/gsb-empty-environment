import type {
  ConditionObject,
  ConditionValue,
  ExportsField,
  ResolveResult,
  Trace,
  TraceAttempt,
} from "./types.ts";

type ResolveOutcome =
  | { kind: "target"; target: string }
  | { kind: "blocked" }
  | { kind: "none" };

/**
 * 由包名和裸说明符推导相对子路径。
 * `pkg` / `pkg/...` / `@scope/pkg` / `@scope/pkg/...` -> "." / "./..."
 */
export function deriveSubpath(
  specifier: string,
  packageName?: string,
): string {
  if (specifier.startsWith("./") || specifier.startsWith("/")) {
    return specifier.startsWith("./") ? specifier : `.${specifier}`;
  }
  if (!packageName) {
    throw new Error("packageName is required for bare specifiers");
  }
  if (specifier === packageName) return ".";
  const prefix = `${packageName}/`;
  if (!specifier.startsWith(prefix)) {
    throw new Error(
      `specifier "${specifier}" does not belong to package "${packageName}"`,
    );
  }
  return `./${specifier.slice(prefix.length)}`;
}

function normalizeTarget(raw: string): string {
  const target = raw.startsWith("./") ? raw : `./${raw}`;
  let depth = 0;
  for (const segment of target.split("/")) {
    if (segment === "..") {
      depth -= 1;
      if (depth < 0) throw new Error(`target "${raw}" escapes the package`);
    } else if (segment !== "" && segment !== ".") {
      depth += 1;
    }
  }
  return target;
}

function makeTrace(): Trace {
  return { attempts: [], winningKeyPath: null, blockedKeyPath: null };
}

function recordOutcome(
  trace: Trace,
  keyPath: string[],
  outcome: Exclude<ResolveOutcome, { kind: "none" }>,
): ResolveOutcome {
  const attempt: TraceAttempt =
    outcome.kind === "target"
      ? {
          keyPath: [...keyPath],
          active: true,
          outcome: "target",
          target: outcome.target,
        }
      : { keyPath: [...keyPath], active: true, outcome: "blocked", target: null };
  trace.attempts.push(attempt);
  if (attempt.outcome === "target") trace.winningKeyPath = [...keyPath];
  else trace.blockedKeyPath = [...keyPath];
  return outcome;
}

function resolveLeaf(
  value: string | null,
  replacement: string,
): ResolveOutcome {
  if (value === null) return { kind: "blocked" };
  let composed = value;
  if (replacement) {
    if (value.includes("*")) composed = value.replace("*", replacement);
    else if (value.endsWith("/")) composed = `${value}${replacement}`;
  }
  return { kind: "target", target: normalizeTarget(composed) };
}

function resolveValue(
  value: ConditionValue,
  subpath: string,
  replacement: string,
  active: ReadonlySet<string>,
  trace: Trace,
  parentPath: string[] = [],
  leafRecorded = false,
): ResolveOutcome {
  if (value === null || typeof value === "string") {
    const outcome = resolveLeaf(value, replacement);
    if (leafRecorded && outcome.kind !== "none")
      recordOutcome(trace, parentPath, outcome);
    return outcome;
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      const outcome = resolveValue(
        item,
        subpath,
        replacement,
        active,
        trace,
        parentPath,
        leafRecorded,
      );
      if (outcome.kind !== "none") return outcome;
    }
    return { kind: "none" };
  }
  const isSubpathMap = Object.keys(value).some((key) => key.startsWith("."));
  return isSubpathMap
    ? lookupSubpath(value, subpath, active, trace, parentPath, leafRecorded)
    : resolveConditions(value, active, subpath, trace, parentPath);
}

/**
 * 在子路径映射对象中查找说明符。
 * 精确键优先，其后按键长度降序匹配通配/尾部斜杠键。
 */
function lookupSubpath(
  exportsObj: ConditionObject,
  subpath: string,
  active: ReadonlySet<string>,
  trace: Trace,
  parentPath: string[] = [],
  leafRecorded = false,
): ResolveOutcome {
  const entries = Object.entries(exportsObj);

  if (Object.hasOwn(exportsObj, subpath)) {
    const keyPath = [...parentPath, subpath];
    const outcome = resolveValue(
      exportsObj[subpath],
      subpath,
      "",
      active,
      trace,
      keyPath,
      leafRecorded,
    );
    if (outcome.kind === "none") return { kind: "none" };
    return outcome;
  }

  const patternKeys = entries
    .filter(([key]) => key.includes("*"))
    .sort((a, b) => b[0].length - a[0].length);
  for (const [key] of patternKeys) {
    const starIndex = key.indexOf("*");
    const head = key.slice(0, starIndex);
    const tail = key.slice(starIndex + 1);
    if (
      subpath.startsWith(head) &&
      subpath.endsWith(tail) &&
      subpath.length >= head.length + tail.length
    ) {
      const replacement = subpath.slice(
        head.length,
        subpath.length - tail.length,
      );
      const keyPath = [...parentPath, key];
      const outcome = resolveValue(
        exportsObj[key],
        subpath,
        replacement,
        active,
        trace,
        keyPath,
        leafRecorded,
      );
      if (outcome.kind !== "none")
        return outcome;
    }
  }

  const trailingKeys = entries
    .filter(([key]) => key.endsWith("/") && !key.includes("*"))
    .sort((a, b) => b[0].length - a[0].length);
  for (const [key] of trailingKeys) {
    if (subpath.startsWith(key)) {
      const replacement = subpath.slice(key.length);
      const keyPath = [...parentPath, key];
      const outcome = resolveValue(
        exportsObj[key],
        subpath,
        replacement,
        active,
        trace,
        keyPath,
        leafRecorded,
      );
      if (outcome.kind !== "none")
        return outcome;
    }
  }

  return { kind: "none" };
}

/**
 * 解析条件对象：按对象自身键顺序取第一个匹配的活动键。
 * 嵌套无匹配则回到上层下一个键；命中 null 直接封锁，不回溯。
 */
function resolveConditions(
  obj: ConditionObject,
  active: ReadonlySet<string>,
  subpath: string,
  trace: Trace,
  parentPath: string[] = [],
): ResolveOutcome {
  for (const key of Object.keys(obj)) {
    if (!active.has(key)) continue;
    const keyPath = [...parentPath, key];
    const outcome = resolveValue(
      obj[key],
      subpath,
      "",
      active,
      trace,
      keyPath,
      true,
    );
    if (outcome.kind === "none") {
      trace.attempts.push({
        keyPath,
        active: true,
        outcome: "nested-no-match",
        target: null,
      });
      continue;
    }
    return outcome;
  }
  return { kind: "none" };
}

/**
 * 按给定活动条件集合解析一次 exports。
 */
export function resolveExports(
  exportsField: ExportsField,
  subpath: string,
  conditions: readonly string[],
): ResolveResult {
  const trace = makeTrace();
  const active = new Set(conditions);
  const outcome = resolveValue(
    exportsField,
    subpath,
    "",
    active,
    trace,
    [],
    false,
  );
  if (outcome.kind === "target") {
    if (trace.winningKeyPath === null) recordOutcome(trace, [], outcome);
    return { found: true, blocked: false, target: outcome.target, trace };
  }
  if (outcome.kind === "blocked") {
    if (trace.attempts.length === 0) recordOutcome(trace, [], outcome);
    return { found: false, blocked: true, target: null, trace };
  }
  return { found: false, blocked: false, target: null, trace };
}
