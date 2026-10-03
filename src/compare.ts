import {
  ALL_VALUE_TYPES,
  effectiveEnum,
  escapePointer,
  findUnsupported,
  has,
  stableStringify,
  valueTypes,
  type JsonSchema,
  type SchemaObject,
  type UnsupportedReason,
} from "./schema.ts";
import { accepts, acceptsNonEnum, explainReject } from "./evaluate.ts";
import {
  findNonInteger,
  intBounds,
  isEmptyInterval,
  lenInterval,
  numInterval,
  sizeInterval,
  type Interval,
} from "./intervals.ts";
import {
  buildObject,
  enumerateValues,
  makeInside,
  makeOfType,
  stringOfLength,
} from "./values.ts";

export interface Issue {
  /** JSON Pointer into an instance: where the rejected value lives. */
  instancePath: string;
  /** JSON Pointer into the new contract: the rule that rejects it. */
  rulePath: string;
  message: string;
  /** A concrete value accepted by the old contract but rejected by the new one. */
  counterexample: unknown;
}

export type CompareResult =
  | { status: "compatible"; issues: Issue[] }
  | { status: "breaking"; issues: Issue[]; unsupported?: UnsupportedReason[] }
  | { status: "unknown"; issues: Issue[]; unsupported: UnsupportedReason[] };

/**
 * Decide whether the new contract accepts every value the old contract
 * allows (old ⊆ new over the supported schema subset).
 *
 *  - "compatible": proven for every value in the supported subset.
 *  - "breaking":   concrete counterexamples attached (instancePath + rulePath).
 *  - "unknown":    either contract uses keywords outside the supported
 *                  subset; we never report "cannot prove" as compatible.
 */
export function compareContracts(oldSchema: JsonSchema, newSchema: JsonSchema): CompareResult {
  const oldU = findUnsupported(oldSchema, "");
  const newU = findUnsupported(newSchema, "");
  if (oldU.length > 0) {
    // The old contract may accept values we cannot model: witnesses and
    // proofs are both unreliable.
    return { status: "unknown", issues: [], unsupported: [...oldU, ...newU] };
  }
  const issues = checkCompat(oldSchema, newSchema, "", "");
  if (newU.length > 0) {
    // Counterexamples found on the supported part remain valid breaks
    // (rejected by a supported keyword => rejected overall). But a clean
    // pass proves nothing, since unsupported keywords may reject more.
    return issues.length > 0
      ? { status: "breaking", issues, unsupported: newU }
      : { status: "unknown", issues: [], unsupported: newU };
  }
  return issues.length > 0
    ? { status: "breaking", issues }
    : { status: "compatible", issues: [] };
}

function issue(
  instancePath: string,
  rulePath: string,
  message: string,
  counterexample: unknown,
): Issue {
  return { instancePath, rulePath, message, counterexample };
}

export function checkCompat(
  oldSchema: JsonSchema,
  newSchema: JsonSchema,
  inst: string,
  rule: string,
): Issue[] {
  if (oldSchema === false) return []; // old accepts nothing: subset of anything
  if (newSchema === true) return []; // new accepts everything: superset of anything
  if (newSchema === false) {
    const w = makeInside(oldSchema);
    return w.ok
      ? [
          issue(
            inst,
            rule === "" ? "/" : rule,
            "new contract is `false` and accepts nothing, but the old contract accepts values",
            w.value,
          ),
        ]
      : [];
  }
  const oldS: SchemaObject = oldSchema === true ? {} : oldSchema;
  const newS: SchemaObject = newSchema === true ? {} : newSchema;

  // --- finite enumeration on the old side: test each accepted value --------
  const oldEnum = effectiveEnum(oldS);
  if (oldEnum !== undefined) {
    const issues: Issue[] = [];
    const seen = new Set<string>();
    for (const v of oldEnum) {
      const key = stableStringify(v);
      if (seen.has(key)) continue;
      seen.add(key);
      if (!acceptsNonEnum(oldS, v)) continue;
      if (!accepts(newS, v)) {
        issues.push(
          issue(
            inst,
            explainReject(newS, v, rule),
            `value ${JSON.stringify(v)} allowed by the old contract is rejected by the new contract`,
            v,
          ),
        );
      }
    }
    return issues;
  }

  // --- finite enumeration on the new side: old must fit inside it ----------
  const newEnum = effectiveEnum(newS);
  if (newEnum !== undefined) {
    const acceptedNew = newEnum.filter((v) => acceptsNonEnum(newS, v));
    const limit = acceptedNew.length;
    let count = 0;
    for (const v of enumerateValues(oldS)) {
      if (!accepts(newS, v)) {
        return [
          issue(
            inst,
            explainReject(newS, v, rule),
            `value ${JSON.stringify(v)} allowed by the old contract is rejected by the new contract`,
            v,
          ),
        ];
      }
      count++;
      if (count > limit) {
        throw new Error(
          "internal invariant violated: enumeration produced more distinct accepted values than the new contract allows",
        );
      }
    }
    return [];
  }

  // --- structural comparison ----------------------------------------------
  const issues: Issue[] = [];
  const oldT = valueTypes(oldS);
  const newT = valueTypes(newS);

  for (const vt of ALL_VALUE_TYPES) {
    if (oldT.has(vt) && !newT.has(vt)) {
      const w = makeOfType(oldS, vt);
      if (w.ok) {
        issues.push(
          issue(inst, rule + "/type", `old contract allows values of type "${vt}", new contract does not`, w.value),
        );
      }
    }
  }

  // numeric intervals
  if (oldT.has("integer") && newT.has("integer")) {
    const o = intBounds(numInterval(oldS));
    const n = intBounds(numInterval(newS));
    if (o.lo <= o.hi) {
      if (n.lo > n.hi) {
        issues.push(issue(inst, rule + "/minimum", "new contract accepts no integers", o.lo));
      } else {
        if (o.lo < n.lo) {
          const w = o.lo === -Infinity ? n.lo - 1 : o.lo;
          issues.push(
            issue(inst, rule + "/minimum", `old contract allows integer ${w} below the new minimum ${n.lo}`, w),
          );
        }
        if (o.hi > n.hi) {
          const w = o.hi === Infinity ? n.hi + 1 : o.hi;
          issues.push(
            issue(inst, rule + "/maximum", `old contract allows integer ${w} above the new maximum ${n.hi}`, w),
          );
        }
      }
    }
  }
  if (oldT.has("number") && newT.has("number")) {
    const w = findNonIntegerOutside(numInterval(oldS), numInterval(newS));
    if (w !== undefined) {
      const n = numInterval(newS);
      const below = n.min !== undefined && (w < n.min || (w === n.min && n.minExcl === true));
      issues.push(
        issue(inst, rule + (below ? "/minimum" : "/maximum"), `old contract allows number ${w} outside the new range`, w),
      );
    }
  }

  // string length
  if (oldT.has("string") && newT.has("string")) {
    const o = intBounds(lenInterval(oldS));
    const n = intBounds(lenInterval(newS));
    if (o.lo <= o.hi) {
      if (n.lo > n.hi) {
        issues.push(issue(inst, rule + "/minLength", "new contract accepts no strings", stringOfLength(o.lo, 0)));
      } else {
        if (o.lo < n.lo) {
          issues.push(
            issue(
              inst,
              rule + "/minLength",
              `old contract allows strings shorter than the new minLength ${n.lo}`,
              stringOfLength(o.lo, 0),
            ),
          );
        }
        if (o.hi > n.hi) {
          const len = o.hi === Infinity ? n.hi + 1 : o.hi;
          issues.push(
            issue(
              inst,
              rule + "/maxLength",
              `old contract allows strings longer than the new maxLength ${n.hi}`,
              stringOfLength(len, 0),
            ),
          );
        }
      }
    }
  }

  // arrays
  if (oldT.has("array") && newT.has("array")) {
    const o = effectiveSizeBounds(oldS);
    const n = effectiveSizeBounds(newS);
    if (o.lo <= o.hi) {
      if (n.lo > n.hi) {
        issues.push(
          issue(inst, rule + "/minItems", "new contract accepts no arrays of the sizes the old contract allows", makeArrayOfSize(oldS, o.lo)),
        );
      } else {
        if (o.lo < n.lo) {
          issues.push(
            issue(inst, rule + "/minItems", `old contract allows arrays shorter than the new minItems ${n.lo}`, makeArrayOfSize(oldS, o.lo)),
          );
        }
        if (o.hi > n.hi) {
          const size = o.hi === Infinity ? n.hi + 1 : o.hi;
          issues.push(
            issue(inst, rule + "/maxItems", `old contract allows arrays longer than the new maxItems ${n.hi}`, makeArrayOfSize(oldS, size)),
          );
        }
      }
      const oldItems: JsonSchema = oldS.items ?? true;
      const newItems: JsonSchema = newS.items ?? true;
      if (o.hi >= 1 && makeInside(oldItems).ok) {
        for (const iss of checkCompat(oldItems, newItems, inst + "/0", rule + "/items")) {
          issues.push({ ...iss, counterexample: liftArrayCounterexample(oldS, iss.counterexample) });
        }
      }
    }
  }

  // objects
  if (oldT.has("object") && newT.has("object")) {
    const baseR = buildObject(oldS);
    if (baseR.ok) {
      const base = baseR.value!;
      const oldReq = new Set(oldS.required ?? []);
      for (const r of newS.required ?? []) {
        if (!oldReq.has(r)) {
          issues.push(
            issue(inst, rule + "/required", `new contract requires property "${r}" which the old contract does not guarantee`, base),
          );
        }
      }
      const oldProps = oldS.properties ?? {};
      const newProps = newS.properties ?? {};
      const keys = new Set([...Object.keys(oldProps), ...Object.keys(newProps)]);
      for (const k of keys) {
        const os: JsonSchema | undefined = has(oldProps, k)
          ? oldProps[k]
          : oldS.additionalProperties === false
            ? undefined
            : (oldS.additionalProperties ?? true);
        if (os === undefined) continue; // old objects never carry this key
        const w = makeInside(os);
        if (!w.ok) continue; // key can never appear in an old-accepted object
        const ns: JsonSchema | undefined = has(newProps, k)
          ? newProps[k]
          : newS.additionalProperties === false
            ? undefined
            : (newS.additionalProperties ?? true);
        const seg = "/" + escapePointer(k);
        if (ns === undefined) {
          issues.push(
            issue(
              inst + seg,
              rule + "/additionalProperties",
              `property "${k}" allowed by the old contract is forbidden by the new contract`,
              { ...base, [k]: w.value },
            ),
          );
        } else {
          const subRule = rule + (has(newProps, k) ? "/properties" + seg : "/additionalProperties");
          for (const iss of checkCompat(os, ns, inst + seg, subRule)) {
            issues.push({ ...iss, counterexample: { ...base, [k]: iss.counterexample } });
          }
        }
      }
      if (oldS.additionalProperties !== false) {
        const os: JsonSchema = oldS.additionalProperties ?? true;
        const w = makeInside(os);
        if (w.ok) {
          const extra = pickExtraKey(oldProps, newProps);
          const seg = "/" + escapePointer(extra);
          if (newS.additionalProperties === false) {
            issues.push(
              issue(
                inst + seg,
                rule + "/additionalProperties",
                "old contract allows additional properties, new contract forbids them",
                { ...base, [extra]: w.value },
              ),
            );
          } else {
            const ns: JsonSchema = newS.additionalProperties ?? true;
            for (const iss of checkCompat(os, ns, inst + seg, rule + "/additionalProperties")) {
              issues.push({ ...iss, counterexample: { ...base, [extra]: iss.counterexample } });
            }
          }
        }
      }
    }
  }

  return issues;
}

/** Non-integer number accepted by interval `o` but rejected by interval `n`. */
function findNonIntegerOutside(o: Interval, n: Interval): number | undefined {
  if (isEmptyInterval(o)) return undefined;
  if (isEmptyInterval(n)) return findNonInteger(o);
  if (n.min !== undefined) {
    const w = findNonInteger({ min: o.min, minExcl: o.minExcl, max: n.min, maxExcl: n.minExcl !== true });
    if (w !== undefined) return w;
  }
  if (n.max !== undefined) {
    const w = findNonInteger({ min: n.max, minExcl: n.maxExcl !== true, max: o.max, maxExcl: o.maxExcl });
    if (w !== undefined) return w;
  }
  return undefined;
}

/** Array sizes the schema can actually accept (accounts for unsatisfiable items). */
function effectiveSizeBounds(s: SchemaObject): { lo: number; hi: number } {
  const { lo, hi } = intBounds(sizeInterval(s));
  if (lo > hi) return { lo: 1, hi: 0 };
  if (!makeInside(s.items ?? true).ok) {
    return lo <= 0 ? { lo: 0, hi: 0 } : { lo: 1, hi: 0 };
  }
  return { lo, hi };
}

function makeArrayOfSize(s: SchemaObject, size: number): unknown[] {
  if (size <= 0) return [];
  const item = makeInside(s.items ?? true);
  return new Array(size).fill(item.ok ? item.value : null);
}

/** Embed an item-level counterexample into an array the old contract accepts. */
function liftArrayCounterexample(oldS: SchemaObject, itemCE: unknown): unknown[] {
  const { lo } = intBounds(sizeInterval(oldS));
  const size = Math.max(1, lo);
  const fill = makeInside(oldS.items ?? true);
  const arr = new Array<unknown>(size).fill(fill.ok ? fill.value : null);
  arr[0] = itemCE;
  return arr;
}

function pickExtraKey(
  oldProps: Record<string, JsonSchema>,
  newProps: Record<string, JsonSchema>,
): string {
  let i = 0;
  while (has(oldProps, `__extra_${i}`) || has(newProps, `__extra_${i}`)) i++;
  return `__extra_${i}`;
}
