import type { AttrDef, AttrInput, AttrValue, Policy } from "./model.ts";
import { evaluate, explanationKey } from "./evaluate.ts";

export type ChangeKind =
  | "unchanged"
  | "allow-to-deny"
  | "deny-to-allow"
  | "explanation-only";

export function validateAttrs(attrs: AttrDef[]): void {
  for (const a of attrs) {
    if (a.kind === "int") {
      if (
        typeof a.min !== "number" ||
        typeof a.max !== "number" ||
        !Number.isInteger(a.min) ||
        !Number.isInteger(a.max) ||
        a.min > a.max
      ) {
        throw new Error(
          `整数属性 "${a.name}" 必须给出有限闭区间 [min, max]（min<=max 且均为整数）；` +
            `无法对无限域做穷举分析，请显式限定分析范围。`,
        );
      }
    } else if (a.kind === "enum") {
      if (!Array.isArray(a.values) || a.values.length === 0) {
        throw new Error(`枚举属性 "${a.name}" 必须给出非空 values 有限集合。`);
      }
    } else {
      throw new Error(`属性 "${(a as AttrDef).name}" 的 kind 必须是 "enum" 或 "int"。`);
    }
  }
}

export interface ClassifiedInput {
  input: AttrInput;
  kind: ChangeKind;
  oldDecision: "allow" | "deny";
  newDecision: "allow" | "deny";
  oldReasons: string[];
  newReasons: string[];
}

export interface AttrSummary {
  attr: string;
  kind: "enum" | "int";
  summary: string;
}

export interface Region {
  kind: Exclude<ChangeKind, "unchanged">;
  count: number;
  coverage: AttrSummary[];
  samples: ClassifiedInput[];
}

export interface CompareReport {
  scope: AttrSummary[];
  totalInputs: number;
  counts: Record<ChangeKind, number>;
  regions: Region[];
}

export function enumerateInputs(attrs: AttrDef[]): AttrInput[] {
  const domains: AttrValue[][] = attrs.map((a) => {
    if (a.kind === "enum") return a.values;
    const vals: number[] = [];
    for (let v = a.min; v <= a.max; v++) vals.push(v);
    return vals;
  });
  const out: AttrInput[] = [];
  const walk = (i: number, acc: AttrInput) => {
    if (i === attrs.length) {
      out.push({ ...acc });
      return;
    }
    for (const v of domains[i]) {
      acc[attrs[i].name] = v;
      walk(i + 1, acc);
    }
  };
  walk(0, {});
  return out;
}

export function scopeSummary(attrs: AttrDef[]): AttrSummary[] {
  return attrs.map((a) =>
    a.kind === "enum"
      ? { attr: a.name, kind: "enum", summary: `{${a.values.join(", ")}}` }
      : { attr: a.name, kind: "int", summary: `整数 [${a.min}, ${a.max}]` },
  );
}

function summarizeAttr(
  def: AttrDef,
  values: AttrValue[],
): AttrSummary {
  if (def.kind === "enum") {
    const uniq = [...new Set(values)];
    const all = uniq.length === def.values.length;
    return {
      attr: def.name,
      kind: "enum",
      summary: all ? "全部取值" : `{${uniq.sort().join(", ")}}`,
    };
  }
  const nums = values.map(Number);
  const lo = Math.min(...nums);
  const hi = Math.max(...nums);
  const full = hi - lo + 1 === def.max - def.min + 1 && nums.length === new Set(nums).size
    ? lo === def.min && hi === def.max
    : false;
  return {
    attr: def.name,
    kind: "int",
    summary: full ? "全部取值" : `[${lo}, ${hi}]（投影区间，含未覆盖点）`,
  };
}

export function comparePolicies(
  attrs: AttrDef[],
  oldPolicy: Policy,
  newPolicy: Policy,
  sampleLimit = 3,
): CompareReport {
  const inputs = enumerateInputs(attrs);
  const counts: Record<ChangeKind, number> = {
    "unchanged": 0,
    "allow-to-deny": 0,
    "deny-to-allow": 0,
    "explanation-only": 0,
  };
  const groups = new Map<Exclude<ChangeKind, "unchanged">, ClassifiedInput[]>();
  for (const input of inputs) {
    const oldRes = evaluate(oldPolicy, input);
    const newRes = evaluate(newPolicy, input);
    let kind: ChangeKind;
    if (oldRes.decision !== newRes.decision) {
      kind = oldRes.decision === "allow" ? "allow-to-deny" : "deny-to-allow";
    } else if (explanationKey(oldRes) !== explanationKey(newRes)) {
      kind = "explanation-only";
    } else {
      kind = "unchanged";
    }
    counts[kind]++;
    if (kind !== "unchanged") {
      const item: ClassifiedInput = {
        input,
        kind,
        oldDecision: oldRes.decision,
        newDecision: newRes.decision,
        oldReasons: oldRes.reasons,
        newReasons: newRes.reasons,
      };
      const list = groups.get(kind) ?? [];
      list.push(item);
      groups.set(kind, list);
    }
  }
  const order: Exclude<ChangeKind, "unchanged">[] = [
    "allow-to-deny",
    "deny-to-allow",
    "explanation-only",
  ];
  const regions: Region[] = [];
  for (const kind of order) {
    const items = groups.get(kind);
    if (!items || items.length === 0) continue;
    const coverage = attrs.map((def) =>
      summarizeAttr(def, items.map((it) => it.input[def.name])),
    );
    regions.push({
      kind,
      count: items.length,
      coverage,
      samples: items.slice(0, sampleLimit),
    });
  }
  return {
    scope: scopeSummary(attrs),
    totalInputs: inputs.length,
    counts,
    regions,
  };
}
