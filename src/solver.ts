import type { Group, Json, Schema, UndecidedPolicy } from './types.ts';
import {
  conditionFields,
  equalsLiterals,
  evalCondition,
  type FieldGetter,
} from './condition.ts';
import { deepEqual } from './presence.ts';
import { assertSchema } from './validate.ts';

export const ABSENT: unique symbol = Symbol('absent');
export const OTHER: unique symbol = Symbol('other');

export type CellValue = Json | typeof ABSENT | typeof OTHER;
export type Assignment = Map<string, CellValue>;

export interface Witness {
  assignment: Assignment;
  present: string[];
}

export type SolveResult = { status: 'sat'; witness: Witness } | { status: 'unsat' };

interface CompiledGroup extends Group {
  index: number;
  rulePath: string;
}

function sameCell(a: CellValue, b: CellValue): boolean {
  if (typeof a === 'symbol' || typeof b === 'symbol') return a === b;
  return deepEqual(a, b);
}

export class Solver {
  readonly fields: string[];
  private readonly baseDomains: Map<string, CellValue[]>;
  private domains: Map<string, CellValue[]>;
  private readonly groups: CompiledGroup[];
  private readonly policy: UndecidedPolicy;
  private trail: Array<[string, CellValue[]]> = [];
  private marks: number[] = [];

  constructor(schema: Schema) {
    assertSchema(schema);
    this.policy = schema.onUndecidedCondition ?? 'disabled';
    this.groups = (schema.groups ?? []).map((g, i) => ({
      ...g,
      index: i,
      rulePath: `/groups/${i}`,
    }));
    const fieldSet = new Set<string>();
    const literals = new Map<string, Json[]>();
    for (const g of this.groups) {
      for (const f of g.fields) fieldSet.add(f);
      if (g.when) {
        for (const f of conditionFields(g.when)) fieldSet.add(f);
        for (const [f, vs] of equalsLiterals(g.when)) {
          const arr = literals.get(f) ?? [];
          for (const v of vs) if (!arr.some((x) => deepEqual(x, v))) arr.push(v);
          literals.set(f, arr);
        }
      }
    }
    this.fields = [...fieldSet].sort();
    this.baseDomains = new Map();
    for (const f of this.fields) {
      this.baseDomains.set(f, [ABSENT, OTHER, ...(literals.get(f) ?? [])]);
    }
    this.domains = new Map([...this.baseDomains].map(([f, d]) => [f, [...d]]));
  }

  baseDomain(field: string): CellValue[] {
    return [...(this.baseDomains.get(field) ?? [ABSENT])];
  }

  domainOf(field: string): CellValue[] {
    return [...(this.domains.get(field) ?? [ABSENT])];
  }

  push(): void {
    this.marks.push(this.trail.length);
  }

  pop(): void {
    const mark = this.marks.pop();
    if (mark === undefined) throw new Error('pop without push');
    while (this.trail.length > mark) {
      const [field, prev] = this.trail.pop()!;
      this.domains.set(field, prev);
    }
  }

  get depth(): number {
    return this.marks.length;
  }

  assume(field: string, value: CellValue): boolean {
    if (!this.baseDomains.has(field)) throw new Error(`unknown field: ${field}`);
    if (!this.baseDomains.get(field)!.some((v) => sameCell(v, value))) {
      throw new Error(`value outside base domain of ${field}`);
    }
    return this.restrict(field, [value]) && this.propagate();
  }

  private restrict(field: string, allowed: CellValue[]): boolean {
    const cur = this.domains.get(field)!;
    const next = cur.filter((v) => allowed.some((a) => sameCell(a, v)));
    if (next.length === 0) return false;
    if (next.length === cur.length) return true;
    this.trail.push([field, cur]);
    this.domains.set(field, next);
    return true;
  }

  private getter: FieldGetter = (field) => {
    const d = this.domains.get(field);
    if (!d) return { state: 'absent' };
    if (d.length > 1) return { state: 'unknown' };
    const v = d[0];
    if (v === ABSENT) return { state: 'absent' };
    return { state: 'value', value: v };
  };

  private propagate(): boolean {
    let changed = true;
    while (changed) {
      changed = false;
      for (const g of this.groups) {
        if (g.when !== undefined) {
          const r = evalCondition(g.when, this.getter);
          if (r !== true) continue;
        }
        const present: string[] = [];
        const open: string[] = [];
        for (const f of g.fields) {
          const d = this.domains.get(f)!;
          const hasAbsent = d.some((v) => v === ABSENT);
          const hasValue = d.some((v) => v !== ABSENT);
          if (!hasAbsent) present.push(f);
          else if (hasValue) open.push(f);
        }
        switch (g.kind) {
          case 'exactlyOne': {
            if (present.length > 1) return false;
            if (present.length === 1) {
              for (const f of open) {
                if (!this.restrict(f, [ABSENT])) return false;
                changed = true;
              }
            } else {
              if (open.length === 0) return false;
              if (open.length === 1) {
                const d = this.domains.get(open[0])!;
                if (!this.restrict(open[0], d.filter((v) => v !== ABSENT))) return false;
                changed = true;
              }
            }
            break;
          }
          case 'atLeastOne': {
            if (present.length === 0) {
              if (open.length === 0) return false;
              if (open.length === 1) {
                const d = this.domains.get(open[0])!;
                if (!this.restrict(open[0], d.filter((v) => v !== ABSENT))) return false;
                changed = true;
              }
            }
            break;
          }
          case 'atMost': {
            const k = g.k!;
            if (present.length > k) return false;
            if (present.length === k) {
              for (const f of open) {
                if (!this.restrict(f, [ABSENT])) return false;
                changed = true;
              }
            }
            break;
          }
        }
      }
    }
    return true;
  }

  private completeOk(): boolean {
    for (const g of this.groups) {
      let active = true;
      if (g.when !== undefined) {
        const r = evalCondition(g.when, this.getter);
        if (r === false) continue;
        if (r === 'undecided') {
          if (this.policy === 'disabled') continue;
          if (this.policy === 'error') return false;
        }
      }
      const n = g.fields.filter((f) => {
        const d = this.domains.get(f)!;
        return d.length === 1 && d[0] !== ABSENT;
      }).length;
      if (!active) continue;
      if (g.kind === 'exactlyOne' && n !== 1) return false;
      if (g.kind === 'atLeastOne' && n < 1) return false;
      if (g.kind === 'atMost' && n > g.k!) return false;
    }
    return true;
  }

  private currentWitness(): Witness {
    const assignment: Assignment = new Map();
    const present: string[] = [];
    for (const f of this.fields) {
      const v = this.domains.get(f)![0];
      assignment.set(f, v);
      if (v !== ABSENT) present.push(f);
    }
    return { assignment, present };
  }

  private pickField(): string | undefined {
    let best: string | undefined;
    let bestSize = Infinity;
    for (const f of this.fields) {
      const size = this.domains.get(f)!.length;
      if (size > 1 && size < bestSize) {
        best = f;
        bestSize = size;
      }
    }
    return best;
  }

  solve(): SolveResult {
    this.push();
    try {
      return this.search();
    } finally {
      this.pop();
    }
  }

  private search(): SolveResult {
    if (!this.propagate()) return { status: 'unsat' };
    const field = this.pickField();
    if (field === undefined) {
      return this.completeOk()
        ? { status: 'sat', witness: this.currentWitness() }
        : { status: 'unsat' };
    }
    for (const v of this.domains.get(field)!) {
      this.push();
      if (this.restrict(field, [v])) {
        const r = this.search();
        if (r.status === 'sat') {
          this.pop();
          return r;
        }
      }
      this.pop();
    }
    return { status: 'unsat' };
  }

  enumerate(limit = 10000): Witness[] {
    const out: Witness[] = [];
    this.push();
    try {
      this.enumerateRec(out, limit);
    } finally {
      this.pop();
    }
    return out;
  }

  private enumerateRec(out: Witness[], limit: number): void {
    if (out.length >= limit) return;
    if (!this.propagate()) return;
    const field = this.pickField();
    if (field === undefined) {
      if (this.completeOk()) out.push(this.currentWitness());
      return;
    }
    for (const v of this.domains.get(field)!) {
      this.push();
      if (this.restrict(field, [v])) this.enumerateRec(out, limit);
      this.pop();
      if (out.length >= limit) return;
    }
  }
}

export function jointSatisfiable(schema: Schema): boolean {
  return new Solver(schema).solve().status === 'sat';
}

export function findWitness(schema: Schema): Witness | null {
  const r = new Solver(schema).solve();
  return r.status === 'sat' ? r.witness : null;
}
