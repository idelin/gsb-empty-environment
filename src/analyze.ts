import { indexToValue, type CompiledTable } from "./model.ts";
import {
  boxIsEmpty,
  explainCover,
  findAvoiding,
  intersectBox,
  minimalWitness,
  type Box,
} from "./solver.ts";
import { contains, isEmpty } from "./valset.ts";

export type Assignment = Record<string, string | number>;

export type RowStatus = "wins" | "ties" | "covered" | "vacuous";

export interface CoverStepReport {
  rule: string;
  cells: Box[];
}

export interface RowReport {
  index: number;
  rule: string;
  priority: number;
  status: RowStatus;
  /** Minimal winning input (status "wins") or minimal top-priority tie input (status "ties"). */
  witness?: Assignment;
  witnessIndices?: number[];
  /** Same-priority rules that can still co-hit when this row reaches the top. */
  tiePartners?: string[];
  /** Greedy covering explanation (status "covered"). */
  cover?: CoverStepReport[];
  /** Fields whose condition is unsatisfiable (status "vacuous"). */
  vacuousFields?: string[];
}

export interface ConflictReport {
  rules: [string, string];
  priority: number;
  /** An input hitting both rules simultaneously. */
  witness: Assignment;
  /** True when every co-hit input is shadowed by some higher-priority rule. */
  masked: boolean;
}

export interface AnalysisReport {
  rows: RowReport[];
  conflicts: ConflictReport[];
}

export function assignmentFromIndices(table: CompiledTable, indices: number[]): Assignment {
  const out: Assignment = {};
  table.fields.forEach((fd, f) => {
    out[fd.name] = indexToValue(fd.domain, indices[f]);
  });
  return out;
}

/** Indices of all rules whose conditions match the given input (as domain indices). */
export function matchingRuleIndices(table: CompiledTable, indices: number[]): number[] {
  const out: number[] = [];
  table.rules.forEach((rule, i) => {
    if (rule.box.every((s, f) => contains(s, indices[f]))) out.push(i);
  });
  return out;
}

export function analyze(table: CompiledTable): AnalysisReport {
  const { rules } = table;
  const rows: RowReport[] = [];

  for (let i = 0; i < rules.length; i++) {
    const rule = rules[i];
    const base: RowReport = { index: i, rule: rule.name, priority: rule.priority, status: "wins" };

    const vacuousFields = table.fields
      .filter((_, f) => isEmpty(rule.box[f]))
      .map((fd) => fd.name);
    if (vacuousFields.length > 0) {
      rows.push({ ...base, status: "vacuous", vacuousFields });
      continue;
    }

    const higher = rules.filter((r, j) => j !== i && r.priority > rule.priority);
    const equal = rules.filter((r, j) => j !== i && r.priority === rule.priority);
    const higherBoxes = higher.map((r) => r.box);

    const strict = minimalWitness(rule.box, [...higherBoxes, ...equal.map((r) => r.box)]);
    if (strict) {
      rows.push({
        ...base,
        status: "wins",
        witness: assignmentFromIndices(table, strict),
        witnessIndices: strict,
      });
      continue;
    }

    const tie = minimalWitness(rule.box, higherBoxes);
    if (tie) {
      const tiePartners = equal
        .filter((e) => {
          const inter = intersectBox(rule.box, e.box);
          return inter !== null && findAvoiding(inter, higherBoxes) !== null;
        })
        .map((e) => e.name);
      rows.push({
        ...base,
        status: "ties",
        witness: assignmentFromIndices(table, tie),
        witnessIndices: tie,
        tiePartners,
      });
      continue;
    }

    const steps = explainCover(rule.box, higherBoxes);
    if (steps === null) {
      throw new Error(`internal error: ${rule.name} is neither winnable nor coverable`);
    }
    rows.push({
      ...base,
      status: "covered",
      cover: steps.map((s) => ({ rule: higher[s.index].name, cells: s.cells })),
    });
  }

  const conflicts: ConflictReport[] = [];
  for (let i = 0; i < rules.length; i++) {
    for (let j = i + 1; j < rules.length; j++) {
      const a = rules[i];
      const b = rules[j];
      if (a.priority !== b.priority) continue;
      if (boxIsEmpty(a.box) || boxIsEmpty(b.box)) continue;
      const inter = intersectBox(a.box, b.box);
      if (inter === null) continue;
      const higherBoxes = rules
        .filter((r) => r.priority > a.priority)
        .map((r) => r.box);
      const live = minimalWitness(inter, higherBoxes);
      const witnessIndices = live ?? (minimalWitness(inter, []) as number[]);
      conflicts.push({
        rules: [a.name, b.name],
        priority: a.priority,
        witness: assignmentFromIndices(table, witnessIndices),
        masked: live === null,
      });
    }
  }

  return { rows, conflicts };
}
