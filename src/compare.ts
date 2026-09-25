import { enumerateInputs, projectRegion, type Projection } from "./domain.ts";
import { evalRuleSet } from "./eval.ts";
import type {
  Decision,
  DomainSpec,
  Input,
  Primitive,
  RuleSet,
} from "./types.ts";

export type ChangeType = "decision-changed" | "explanation-only" | "unchanged";
export type Direction = "allow->deny" | "deny->allow" | null;

export interface Region {
  changeType: ChangeType;
  /** Non-null only when changeType === "decision-changed". */
  direction: Direction;
  oldEffect: Decision["effect"];
  newEffect: Decision["effect"];
  oldDecision: Decision;
  newDecision: Decision;
  /** One concrete input point drawn from the region. */
  sample: Input;
  /** Number of input points in the region. */
  count: number;
  projections: Projection[];
}

export interface CompareResult {
  totalInputs: number;
  regions: Region[];
  summary: {
    unchanged: number;
    explanationOnly: number;
    allowToDeny: number;
    denyToAllow: number;
  };
}

function signature(d: Decision): string {
  return `${d.effect}|${d.defaulted ? "default" : "rules"}|${d.matchedRuleIds.join(",")}`;
}

/**
 * Semantic comparison over the declared finite domain.
 *
 * This is NOT a textual diff: every concrete input point is evaluated by
 * both rule sets and points are partitioned by their full (effect +
 * explanation) equivalence class. Rule reordering / operand commutation
 * is invisible because evaluation is order independent.
 */
export function compareRuleSets(
  oldSet: RuleSet,
  newSet: RuleSet,
  domain: DomainSpec,
): CompareResult {
  const groups = new Map<
    string,
    { oldD: Decision; newD: Decision; points: Input[] }
  >();

  const inputs = enumerateInputs(domain);
  for (const input of inputs) {
    const oldD = evalRuleSet(oldSet, input);
    const newD = evalRuleSet(newSet, input);
    const key = `${signature(oldD)}#${signature(newD)}`;
    const group = groups.get(key);
    if (group) group.points.push(input);
    else groups.set(key, { oldD, newD, points: [input] });
  }

  const regions: Region[] = [];
  for (const group of groups.values()) {
    const { oldD, newD, points } = group;
    const effectChanged = oldD.effect !== newD.effect;
    const explanationChanged =
      !effectChanged && signature(oldD) !== signature(newD);
    regions.push({
      changeType: effectChanged
        ? "decision-changed"
        : explanationChanged
          ? "explanation-only"
          : "unchanged",
      direction: effectChanged
        ? (`${oldD.effect}->${newD.effect}` as "allow->deny" | "deny->allow")
        : null,
      oldEffect: oldD.effect,
      newEffect: newD.effect,
      oldDecision: oldD,
      newDecision: newD,
      sample: points[0] as Input,
      count: points.length,
      projections: projectRegion(points, domain),
    });
  }

  const rank: Record<ChangeType, number> = {
    "decision-changed": 0,
    "explanation-only": 1,
    unchanged: 2,
  };
  regions.sort((a, b) => {
    if (rank[a.changeType] !== rank[b.changeType]) {
      return rank[a.changeType] - rank[b.changeType];
    }
    if ((a.direction ?? "") !== (b.direction ?? "")) {
      return (a.direction ?? "") < (b.direction ?? "") ? -1 : 1;
    }
    return b.count - a.count;
  });

  const summary = {
    unchanged: 0,
    explanationOnly: 0,
    allowToDeny: 0,
    denyToAllow: 0,
  };
  for (const r of regions) {
    if (r.changeType === "unchanged") summary.unchanged += r.count;
    else if (r.changeType === "explanation-only") summary.explanationOnly += r.count;
    else if (r.direction === "allow->deny") summary.allowToDeny += r.count;
    else summary.denyToAllow += r.count;
  }

  return { totalInputs: inputs.length, regions, summary };
}

/**
 * Find the region that a concrete input point belongs to. The point must
 * lie inside the declared domain (it is looked up by its full decision
 * signature, not by matching the region's representative sample).
 */
export function regionForInput(
  result: CompareResult,
  oldSet: RuleSet,
  newSet: RuleSet,
  input: Input,
): Region {
  const key = `${signature(evalRuleSet(oldSet, input))}#${signature(evalRuleSet(newSet, input))}`;
  const region = result.regions.find(
    (r) =>
      `${signature(r.oldDecision)}#${signature(r.newDecision)}` === key,
  );
  if (!region) throw new Error("input is outside the analyzed domain");
  return region;
}

export function formatValue(v: Primitive): string {
  return typeof v === "string" ? v : String(v);
}
