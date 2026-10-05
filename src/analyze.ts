import type { DecisionTable, FieldDef, Scalar } from './model.ts';
import type { Box } from './boxes.ts';
import {
  boxIsEmpty,
  boxVolume,
  boxesIntersect,
  cloneBox,
  fieldDomain,
  intersectBoxes,
  regionVolume,
  ruleBox,
  sortedValues,
  subtractAll,
} from './boxes.ts';

export interface Witness {
  // Per-field guarantee: constrained fields list their allowed values,
  // unconstrained fields are 'any'. Every completion of this pattern wins.
  pattern: Record<string, Scalar[] | 'any'>;
  // One concrete input that makes the rule win.
  input: Record<string, Scalar>;
  constrainedFields: number;
  // Number of concrete inputs matched by `pattern`; all are guaranteed wins.
  guaranteedInputs: string;
}

export interface CoverEntry {
  ruleId: string;
  relation: 'higher-priority' | 'equal-priority';
  // The part of this rule's hit region covered by the covering rule
  // (only fields where the region is narrower than the whole domain).
  region: Record<string, Scalar[]>;
}

export interface RuleAnalysis {
  ruleId: string;
  priority: number;
  action?: string;
  status: 'winnable' | 'covered' | 'unsatisfiable';
  witness?: Witness;
  coveredBy?: CoverEntry[];
}

export interface ConflictPair {
  ruleIds: [string, string];
  priority: number;
  // true: some input lets both rules tie at the top (no higher rule decides);
  // false: every shared input is already decided by a higher-priority rule.
  effective: boolean;
  witnessInput: Record<string, Scalar>;
}

export interface AnalysisReport {
  tableName?: string;
  fieldCount: number;
  ruleCount: number;
  rules: RuleAnalysis[];
  conflicts: ConflictPair[];
}

export interface AnalyzeOptions {
  // Safety cap on intermediate boxes during set differences.
  maxBoxes?: number;
}

const DEFAULT_MAX_BOXES = 50_000;

export function analyzeTable(table: DecisionTable, opts: AnalyzeOptions = {}): AnalysisReport {
  const maxBoxes = opts.maxBoxes ?? DEFAULT_MAX_BOXES;
  const fields = table.fields;
  const fieldNames = fields.map((f) => f.name);
  const fieldByName = new Map(fields.map((f) => [f.name, f]));
  const domains = new Map<string, Scalar[]>(fields.map((f) => [f.name, fieldDomain(f)]));

  const boxes = table.rules.map((r) => ruleBox(table, r));
  const priorities = table.rules.map((r) => r.priority ?? 0);
  const ruleIds = table.rules.map((r) => r.id);

  const checkCap = (boxesList: Box[], ruleId: string) => {
    if (boxesList.length > maxBoxes) {
      throw new Error(
        `rule "${ruleId}": intermediate region exceeded ${maxBoxes} boxes; ` +
        `table is too fragmented for symbolic analysis`,
      );
    }
  };

  const rulesOut: RuleAnalysis[] = [];

  for (let i = 0; i < table.rules.length; i++) {
    const rule = table.rules[i];
    const base: RuleAnalysis = {
      ruleId: rule.id,
      priority: priorities[i],
      action: rule.action,
      status: 'winnable',
    };
    rulesOut.push(base);

    if (boxIsEmpty(boxes[i])) {
      base.status = 'unsatisfiable';
      continue;
    }

    // A rule truly wins only where no rule with priority >= its own matches.
    const competitors: number[] = [];
    for (let j = 0; j < table.rules.length; j++) {
      if (j !== i && priorities[j] >= priorities[i]) competitors.push(j);
    }

    let region: Box[] = [boxes[i]];
    for (const j of competitors) {
      region = subtractAll(region, boxes[j], fieldNames);
      checkCap(region, rule.id);
      if (region.length === 0) break;
    }

    if (region.length > 0) {
      base.status = 'winnable';
      base.witness = buildWitness(
        region,
        boxes[i],
        competitors.map((j) => boxes[j]),
        fields,
        fieldByName,
        domains,
      );
    } else {
      base.status = 'covered';
      base.coveredBy = explainCoverage(
        boxes[i],
        competitors.map((j) => ({ idx: j, box: boxes[j] })),
        fieldNames,
        fields,
        fieldByName,
        priorities,
        ruleIds,
        i,
      );
    }
  }

  const conflicts: ConflictPair[] = [];
  for (let i = 0; i < table.rules.length; i++) {
    for (let j = i + 1; j < table.rules.length; j++) {
      if (priorities[i] !== priorities[j]) continue;
      if (boxIsEmpty(boxes[i]) || boxIsEmpty(boxes[j])) continue;
      const inter = intersectBoxes(boxes[i], boxes[j]);
      if (!inter) continue;
      // Prefer a witness not decided by any higher-priority rule.
      let effectiveRegion: Box[] = [inter];
      for (let k = 0; k < table.rules.length; k++) {
        if (priorities[k] > priorities[i]) {
          effectiveRegion = subtractAll(effectiveRegion, boxes[k], fieldNames);
          if (effectiveRegion.length === 0) break;
        }
      }
      const effective = effectiveRegion.length > 0;
      const source = effective ? effectiveRegion[0] : inter;
      conflicts.push({
        ruleIds: [ruleIds[i], ruleIds[j]],
        priority: priorities[i],
        effective,
        witnessInput: concreteInput(source, fields, fieldByName, domains),
      });
    }
  }

  return {
    tableName: table.name,
    fieldCount: fields.length,
    ruleCount: table.rules.length,
    rules: rulesOut,
    conflicts,
  };
}

function concreteInput(
  box: Box,
  fields: FieldDef[],
  fieldByName: Map<string, FieldDef>,
  domains: Map<string, Scalar[]>,
): Record<string, Scalar> {
  const input: Record<string, Scalar> = {};
  for (const f of fields) {
    const allowed = box.get(f.name)!;
    const domain = domains.get(f.name)!;
    input[f.name] = allowed.size === domain.length
      ? domain[0]
      : sortedValues(fieldByName.get(f.name)!, allowed)[0];
  }
  return input;
}

// Greedily relax each winning-region box: drop a field constraint when the
// fully-unconstrained field still keeps the box inside the winning region.
// Keep the box with fewest constrained fields (ties: largest volume).
function buildWitness(
  region: Box[],
  rowBox: Box,
  competitorBoxes: Box[],
  fields: FieldDef[],
  fieldByName: Map<string, FieldDef>,
  domains: Map<string, Scalar[]>,
): Witness {
  let best: Box | null = null;
  let bestConstrained = Infinity;
  let bestVolume = -1n;

  for (const startBox of region) {
    const relaxed = cloneBox(startBox);
    for (const f of fields) {
      const domain = domains.get(f.name)!;
      if (relaxed.get(f.name)!.size === domain.length) continue;
      // Relaxing must stay inside the rule's own hit region.
      if (rowBox.get(f.name)!.size !== domain.length) continue;
      const candidate = cloneBox(relaxed);
      candidate.set(f.name, new Set(domain));
      if (competitorBoxes.some((c) => boxesIntersect(candidate, c))) continue;
      relaxed.set(f.name, new Set(domain));
    }
    let constrained = 0;
    for (const f of fields) {
      if (relaxed.get(f.name)!.size !== domains.get(f.name)!.length) constrained++;
    }
    const vol = boxVolume(relaxed);
    if (constrained < bestConstrained || (constrained === bestConstrained && vol > bestVolume)) {
      best = relaxed;
      bestConstrained = constrained;
      bestVolume = vol;
    }
  }

  const box = best!;
  const pattern: Record<string, Scalar[] | 'any'> = {};
  for (const f of fields) {
    const allowed = box.get(f.name)!;
    pattern[f.name] = allowed.size === domains.get(f.name)!.length
      ? 'any'
      : sortedValues(fieldByName.get(f.name)!, allowed);
  }
  return {
    pattern,
    input: concreteInput(box, fields, fieldByName, domains),
    constrainedFields: bestConstrained,
    guaranteedInputs: bestVolume.toString(),
  };
}

// Greedy set cover over symbolic boxes, then redundancy elimination.
function explainCoverage(
  rowBox: Box,
  competitors: { idx: number; box: Box }[],
  fieldNames: string[],
  fields: FieldDef[],
  fieldByName: Map<string, FieldDef>,
  priorities: number[],
  ruleIds: string[],
  selfIdx: number,
): CoverEntry[] {
  const byIdx = new Map(competitors.map((c) => [c.idx, c.box]));
  const remaining = new Set(competitors.map((c) => c.idx));
  const cover: number[] = [];
  let rest: Box[] = [rowBox];

  while (rest.length > 0) {
    let bestIdx = -1;
    let bestReduction = 0n;
    let bestRest: Box[] = [];
    for (const idx of remaining) {
      const next = subtractAll(rest, byIdx.get(idx)!, fieldNames);
      const reduction = regionVolume(rest) - regionVolume(next);
      if (reduction > bestReduction) {
        bestReduction = reduction;
        bestIdx = idx;
        bestRest = next;
      }
    }
    if (bestIdx < 0) break; // unreachable if coverage was verified
    cover.push(bestIdx);
    remaining.delete(bestIdx);
    rest = bestRest;
  }

  // Drop redundant cover members.
  for (let k = cover.length - 1; k >= 0; k--) {
    const others = cover.filter((_, idx) => idx !== k);
    let check: Box[] = [rowBox];
    for (const idx of others) {
      check = subtractAll(check, byIdx.get(idx)!, fieldNames);
      if (check.length === 0) break;
    }
    if (check.length === 0) cover.splice(k, 1);
  }

  return cover.map((idx) => {
    const regionBox = intersectBoxes(rowBox, byIdx.get(idx)!)!;
    const region: Record<string, Scalar[]> = {};
    for (const f of fields) {
      const allowed = regionBox.get(f.name)!;
      if (allowed.size !== fieldDomain(f).length) {
        region[f.name] = sortedValues(fieldByName.get(f.name)!, allowed);
      }
    }
    return {
      ruleId: ruleIds[idx],
      relation: priorities[idx] > priorities[selfIdx] ? 'higher-priority' : 'equal-priority',
      region,
    };
  });
}
