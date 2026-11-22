import { digestValue } from "./canonical.ts";
import type {
  Scalar,
  ScriptPolicy,
  ScriptRange,
  ScriptTable,
} from "./types.ts";
import { assertUnicodeScalar } from "./unicode.ts";

const DEFAULT_COMMON_SCRIPTS = [
  "Common",
  "Inherited",
  "Zyyy",
  "Zinh",
  "Unknown",
] as const;

export function assertScriptTable(table: ScriptTable): void {
  if (!table || typeof table.version !== "string" || table.version.length === 0) {
    throw new TypeError("script table needs a non-empty version");
  }
  if (!Array.isArray(table.ranges)) {
    throw new TypeError("script table ranges must be an array");
  }
  table.ranges.forEach((range, index) => {
    const context = `script range ${index}`;
    assertUnicodeScalar(range.start, context);
    assertUnicodeScalar(range.end, context);
    if (range.start > range.end) {
      throw new TypeError(`${context}: start is after end`);
    }
    if (typeof range.script !== "string" || range.script.length === 0) {
      throw new TypeError(`${context}: script must be a non-empty string`);
    }
    if (range.extensions !== undefined) {
      if (
        !Array.isArray(range.extensions) ||
        range.extensions.length === 0 ||
        range.extensions.some(
          (script) => typeof script !== "string" || script.length === 0,
        )
      ) {
        throw new TypeError(
          `${context}: extensions must be a non-empty string array`,
        );
      }
    }
  });
  if (
    table.commonScripts !== undefined &&
    (!Array.isArray(table.commonScripts) ||
      table.commonScripts.some(
        (script) => typeof script !== "string" || script.length === 0,
      ))
  ) {
    throw new TypeError("script table commonScripts must be a string array");
  }
  if (
    table.defaultScript !== undefined &&
    (typeof table.defaultScript !== "string" ||
      table.defaultScript.length === 0)
  ) {
    throw new TypeError("script table defaultScript must be a non-empty string");
  }
  const sorted = [...table.ranges].sort((a, b) => a.start - b.start);
  for (let i = 1; i < sorted.length; i++) {
    if (sorted[i].start <= sorted[i - 1].end) {
      throw new TypeError("script ranges overlap");
    }
  }
}

export function scriptsFromRanges(
  version: string,
  ranges: readonly ScriptRange[],
  options: {
    readonly commonScripts?: readonly string[];
    readonly defaultScript?: string;
    readonly meta?: Readonly<Record<string, unknown>>;
  } = {},
): ScriptTable {
  const sorted = ranges
    .map((range) => ({ ...range }))
    .sort((a, b) => a.start - b.start);
  const table: ScriptTable = {
    version,
    ranges: sorted,
    ...(options.commonScripts ? { commonScripts: options.commonScripts } : {}),
    ...(options.defaultScript ? { defaultScript: options.defaultScript } : {}),
    ...(options.meta ? { meta: options.meta } : {}),
  };
  assertScriptTable(table);
  return table;
}

export interface ScriptInfo {
  readonly script: string;
  readonly extensions?: readonly string[];
}

const sortedRangeCache = new WeakMap<ScriptTable, readonly ScriptRange[]>();
const validatedTables = new WeakSet<ScriptTable>();

function ensureScriptTable(table: ScriptTable): void {
  if (validatedTables.has(table)) return;
  assertScriptTable(table);
  validatedTables.add(table);
}

function sortedRanges(table: ScriptTable): readonly ScriptRange[] {
  const cached = sortedRangeCache.get(table);
  if (cached) return cached;
  const sorted = [...table.ranges].sort((a, b) => a.start - b.start);
  sortedRangeCache.set(table, sorted);
  return sorted;
}

export function scriptInfo(table: ScriptTable, scalar: Scalar): ScriptInfo | null {
  ensureScriptTable(table);
  const ranges = sortedRanges(table);
  let low = 0;
  let high = ranges.length - 1;
  while (low <= high) {
    const middle = (low + high) >>> 1;
    const range = ranges[middle];
    if (scalar < range.start) {
      high = middle - 1;
    } else if (scalar > range.end) {
      low = middle + 1;
    } else {
      return {
        script: range.script,
        ...(range.extensions ? { extensions: range.extensions } : {}),
      };
    }
  }
  if (table.defaultScript) {
    return { script: table.defaultScript };
  }
  return null;
}

function neutralScripts(table: ScriptTable): ReadonlySet<string> {
  return new Set(table.commonScripts ?? DEFAULT_COMMON_SCRIPTS);
}

/**
 * Script_Extensions are authoritative when present.  Neutral scripts are
 * removed, leaving the set of script choices for this scalar.
 */
export function effectiveScripts(
  table: ScriptTable,
  scalar: Scalar,
): readonly string[] {
  const info = scriptInfo(table, scalar);
  if (!info) return [];
  const neutral = neutralScripts(table);
  const candidates = info.extensions ?? [info.script];
  const output: string[] = [];
  for (const script of candidates) {
    if (!neutral.has(script) && !output.includes(script)) output.push(script);
  }
  return output;
}

function unique(values: readonly string[]): string[] {
  return [...new Set(values)];
}

/**
 * Check a suffix script policy against a label.
 *
 * A scalar with Script_Extensions {Latin, Greek} can participate as either
 * script.  For single-script policies the valid choices are therefore the
 * intersection of each scalar's permitted choice set, not the union.
 */
export function scriptPolicyHolds(
  table: ScriptTable,
  policy: ScriptPolicy | undefined,
  scalars: Iterable<Scalar>,
): boolean {
  if (!policy) return true;
  ensureScriptTable(table);
  if (!Array.isArray(policy.allowed)) {
    throw new TypeError("script policy allowed must be an array");
  }
  const allowed = new Set(policy.allowed);
  const disallowed = new Set(policy.disallowed ?? []);
  const choices: string[][] = [];

  for (const scalar of scalars) {
    const effective = effectiveScripts(table, scalar);
    if (effective.length === 0) continue;
    const options = unique(
      effective.filter(
        (script) => allowed.has(script) && !disallowed.has(script),
      ),
    );
    if (options.length === 0) return false;
    choices.push(options);
  }

  if (choices.length === 0) {
    return policy.allowNeutralOnly ?? true;
  }

  const min = policy.minScripts ?? 1;
  const max = policy.maxScripts ?? Number.POSITIVE_INFINITY;

  if (policy.singleScript ?? true) {
    let intersection = choices[0].slice();
    for (let i = 1; i < choices.length && intersection.length > 0; i++) {
      const next = new Set(choices[i]);
      intersection = intersection.filter((script) => next.has(script));
    }
    if (intersection.length === 0) return false;
    // A non-empty intersection means one common script can be chosen for the
    // whole label.
    return 1 >= min && 1 <= max;
  }

  const union = new Set<string>();
  for (const options of choices) {
    for (const script of options) union.add(script);
  }
  if (max === Number.POSITIVE_INFINITY && min <= 1) return true;

  // Existence of an assignment whose realised script set fits [min, max].
  // Script sets are tiny, so a bounded set-of-sets dynamic program is cheap.
  const cap = Number.isFinite(max) ? max : union.size;
  if (cap < min) return false;
  let states = new Set<string>([""]);
  for (const options of choices) {
    const next = new Set<string>();
    for (const state of states) {
      const current: string[] =
        state.length === 0 ? [] : state.split("\u0000");
      for (const option of options) {
        const merged = current.includes(option)
          ? current.slice()
          : [...current, option].sort();
        if (merged.length > cap) continue;
        next.add(merged.join("\u0000"));
      }
    }
    states = next;
    if (states.size === 0) return false;
  }
  return [...states].some((state) => {
    const size = state.length === 0 ? 0 : state.split("\u0000").length;
    return size >= min && size <= cap;
  });
}

export function scriptTableDigest(table: ScriptTable): string {
  ensureScriptTable(table);
  return digestValue({
    kind: "script-table",
    version: table.version,
    ranges: sortedRanges(table).map((range) => ({
      start: range.start,
      end: range.end,
      script: range.script,
      extensions: range.extensions ?? null,
    })),
    commonScripts: table.commonScripts ?? null,
    defaultScript: table.defaultScript ?? null,
  });
}
