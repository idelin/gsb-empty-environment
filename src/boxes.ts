import type { Condition, DecisionTable, FieldDef, Rule, Scalar } from './model.ts';

// A Box is a product of per-field allowed-value sets: the set of inputs
// matching a rule (or a fragment of a set difference of such products).
export type Box = Map<string, Set<Scalar>>;

export function fieldDomain(field: FieldDef): Scalar[] {
  if (field.kind === 'enum') return [...field.values];
  const out: number[] = [];
  for (let v = field.min; v <= field.max; v++) out.push(v);
  return out;
}

export function compileCondition(field: FieldDef, cond: Condition): Set<Scalar> {
  const domain = new Set(fieldDomain(field));
  return compileInto(field, cond, domain);
}

function compileInto(field: FieldDef, cond: Condition, domain: Set<Scalar>): Set<Scalar> {
  switch (cond.op) {
    case 'any':
      return new Set(domain);
    case 'eq':
      return domain.has(cond.value) ? new Set([cond.value]) : new Set();
    case 'in':
      return new Set(cond.values.filter((v) => domain.has(v)));
    case 'range': {
      if (field.kind !== 'int') {
        throw new Error(`field "${field.name}": "range" requires an int field`);
      }
      const lo = cond.min ?? field.min;
      const hi = cond.max ?? field.max;
      const out = new Set<Scalar>();
      for (let v = Math.max(lo, field.min); v <= Math.min(hi, field.max); v++) out.add(v);
      return out;
    }
    case 'not': {
      const inner = compileInto(field, cond.cond, domain);
      return new Set([...domain].filter((v) => !inner.has(v)));
    }
    case 'and': {
      let acc = new Set(domain);
      for (const c of cond.conds) {
        const s = compileInto(field, c, domain);
        acc = new Set([...acc].filter((v) => s.has(v)));
      }
      return acc;
    }
    case 'or': {
      const acc = new Set<Scalar>();
      for (const c of cond.conds) {
        for (const v of compileInto(field, c, domain)) acc.add(v);
      }
      return acc;
    }
  }
}

export function fullBox(fields: FieldDef[]): Box {
  const box: Box = new Map();
  for (const f of fields) box.set(f.name, new Set(fieldDomain(f)));
  return box;
}

export function ruleBox(table: DecisionTable, rule: Rule): Box {
  const box = fullBox(table.fields);
  const byName = new Map(table.fields.map((f) => [f.name, f]));
  for (const [fname, cond] of Object.entries(rule.conditions ?? {})) {
    box.set(fname, compileCondition(byName.get(fname)!, cond));
  }
  return box;
}

export function cloneBox(box: Box): Box {
  const out: Box = new Map();
  for (const [k, v] of box) out.set(k, new Set(v));
  return out;
}

export function boxIsEmpty(box: Box): boolean {
  for (const s of box.values()) if (s.size === 0) return true;
  return false;
}

export function intersectBoxes(a: Box, b: Box): Box | null {
  const out: Box = new Map();
  for (const [k, sa] of a) {
    const sb = b.get(k)!;
    const inter = new Set<Scalar>([...sa].filter((v) => sb.has(v)));
    if (inter.size === 0) return null;
    out.set(k, inter);
  }
  return out;
}

export function boxesIntersect(a: Box, b: Box): boolean {
  for (const [k, sa] of a) {
    const sb = b.get(k)!;
    let any = false;
    for (const v of sa) if (sb.has(v)) { any = true; break; }
    if (!any) return false;
  }
  return true;
}

// box \ obstacle, as a list of disjoint non-empty boxes (splits per field).
export function subtractBox(box: Box, obstacle: Box, fieldNames: string[]): Box[] {
  const out: Box[] = [];
  const current = cloneBox(box);
  for (const f of fieldNames) {
    const cur = current.get(f)!;
    const obs = obstacle.get(f)!;
    const diff = new Set<Scalar>([...cur].filter((v) => !obs.has(v)));
    if (diff.size > 0) {
      const piece = cloneBox(current);
      piece.set(f, diff);
      out.push(piece);
    }
    const inter = new Set<Scalar>([...cur].filter((v) => obs.has(v)));
    if (inter.size === 0) return out;
    current.set(f, inter);
  }
  return out;
}

export function subtractAll(boxes: Box[], obstacle: Box, fieldNames: string[]): Box[] {
  const out: Box[] = [];
  for (const b of boxes) out.push(...subtractBox(b, obstacle, fieldNames));
  return out;
}

export function boxVolume(box: Box): bigint {
  let v = 1n;
  for (const s of box.values()) v *= BigInt(s.size);
  return v;
}

export function regionVolume(boxes: Box[]): bigint {
  let v = 0n;
  for (const b of boxes) v += boxVolume(b);
  return v;
}

export function sortedValues(field: FieldDef, set: Set<Scalar>): Scalar[] {
  if (field.kind === 'int') return [...set].sort((a, b) => (a as number) - (b as number));
  const order = new Map(field.values.map((v, i) => [v, i]));
  return [...set].sort((a, b) => order.get(a)! - order.get(b)!);
}
