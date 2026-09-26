import {
  OBSERVED_TYPES,
  SampleSet,
  collectEvidence,
  unescapeSegment,
  type FieldEvidence,
  type Json,
  type ObservedType,
} from './evidence.ts';
import { IMPLEMENTED_KEYWORDS, validate, type Schema, type ValidationError } from './validate.ts';

export interface Distinguisher {
  instance: Json;
  note: string;
}

export interface CandidateOption {
  rank: number;
  label: string;
  constraint: Record<string, unknown>;
  support: { consistent: number; outOf: number };
  inductive: boolean;
  rationale: string;
  distinguisher: Distinguisher | null;
}

export type Dimension = 'type' | 'presence' | 'nullability' | 'range' | 'length';

export interface DecisionPoint {
  path: string;
  dimension: Dimension;
  options: CandidateOption[];
}

export interface InferenceReport {
  status: 'inferred' | 'unknown';
  sampleCount: number;
  retainedSampleIds: string[];
  decisionPoints: DecisionPoint[];
  unknowns: string[];
  primary: Schema;
  verification: { accepted: number; rejected: number; errors: ValidationError[] };
  guarantees: string[];
  inductions: string[];
}

const OMIT = Symbol('omit');
type Leaf = Json | typeof OMIT;

function displayPath(path: string): string {
  return path === '' ? '(root)' : path;
}

function buildInstance(path: string, value: Leaf): Json {
  if (path === '') return value === OMIT ? null : value;
  const segments = (path.startsWith('/') ? path.slice(1).split('/') : [path]).map(unescapeSegment);
  let acc: Leaf = value;
  for (let i = segments.length - 1; i >= 0; i -= 1) {
    const seg = segments[i];
    if (seg.endsWith('[]')) {
      const base = seg.slice(0, -2);
      const arr: Json = acc === OMIT ? [] : [acc as Json];
      if (base === '') {
        acc = arr;
      } else {
        const obj: Record<string, Json> = {};
        obj[base] = arr;
        acc = obj;
      }
    } else {
      const obj: Record<string, Json> = {};
      if (acc !== OMIT) obj[seg] = acc;
      acc = obj;
    }
  }
  return acc as Json;
}

function rankOptions(options: CandidateOption[]): CandidateOption[] {
  const sorted = [...options].sort((a, b) => b.support.consistent - a.support.consistent);
  sorted.forEach((option, index) => {
    option.rank = index + 1;
  });
  return sorted;
}

function unobservedTypeProbe(observed: ObservedType[]): Json {
  const probes: Array<[ObservedType, Json]> = [
    ['string', 'x'],
    ['integer', 0],
    ['boolean', true],
    ['array', []],
    ['object', {}],
  ];
  for (const [name, probe] of probes) {
    if (!observed.includes(name)) return probe;
  }
  return 0;
}

function typeDecision(field: FieldEvidence): DecisionPoint {
  const nonNull = field.present - field.nullCount;
  const observed = OBSERVED_TYPES.filter((t) => field.typeCounts[t] > 0);
  const options: CandidateOption[] = [];

  if (observed.length === 0) {
    options.push({
      rank: 0,
      label: 'unknown',
      constraint: {},
      support: { consistent: 0, outOf: 0 },
      inductive: false,
      rationale: 'no non-null observation; domain unknown, nothing inferred',
      distinguisher: null,
    });
  } else {
    const onlyIntegers = observed.length === 1 && observed[0] === 'integer';
    if (onlyIntegers) {
      const probe = (field.numericMax?.value ?? 0) + 0.5;
      options.push({
        rank: 0,
        label: 'integer',
        constraint: { type: 'integer' },
        support: { consistent: field.typeCounts.integer, outOf: nonNull },
        inductive: true,
        rationale: `all ${field.typeCounts.integer} observed numbers are integers; integrality is sample-inductive and cannot exclude future decimals`,
        distinguisher: {
          instance: buildInstance(field.path, probe),
          note: `${probe} is rejected by "integer" but accepted by "number"`,
        },
      });
      options.push({
        rank: 0,
        label: 'number',
        constraint: { type: 'number' },
        support: { consistent: nonNull, outOf: nonNull },
        inductive: false,
        rationale: 'widens integer to any number; samples contain no decimal to distinguish',
        distinguisher: {
          instance: buildInstance(field.path, 'x'),
          note: 'a string is rejected by "number" but accepted by "any"',
        },
      });
    } else {
      const union = new Set<ObservedType>(observed);
      if (union.has('number')) union.delete('integer');
      const list = [...union];
      const probe = unobservedTypeProbe(observed);
      options.push({
        rank: 0,
        label: list.length === 1 ? list[0] : `union(${list.join(' | ')})`,
        constraint: { type: list.length === 1 ? list[0] : list },
        support: { consistent: nonNull, outOf: nonNull },
        inductive: true,
        rationale: `only these types appear in ${nonNull} non-null observation(s); unobserved types remain possible`,
        distinguisher: {
          instance: buildInstance(field.path, probe),
          note: `${JSON.stringify(probe)} has a never-observed type: rejected by this option, accepted by "any"`,
        },
      });
    }
    options.push({
      rank: 0,
      label: 'any',
      constraint: {},
      support: { consistent: nonNull, outOf: nonNull },
      inductive: false,
      rationale: 'no type constraint; samples cannot rule out types they never showed',
      distinguisher: null,
    });
  }
  return { path: field.path, dimension: 'type', options: rankOptions(options) };
}

function presenceDecision(field: FieldEvidence): DecisionPoint {
  const opportunities = field.present + field.missing;
  const required: CandidateOption = {
    rank: 0,
    label: 'required',
    constraint: { presence: 'required' },
    support: { consistent: field.present, outOf: opportunities },
    inductive: field.missing === 0,
    rationale:
      field.missing === 0
        ? `present in all ${opportunities} sample(s); absence never observed (sample-inductive)`
        : `absent in ${field.missing} of ${opportunities} sample(s); contradicted by evidence`,
    distinguisher:
      field.missing === 0
        ? {
            instance: buildInstance(field.path, OMIT),
            note: 'omitting the field is rejected by "required" but accepted by "optional"',
          }
        : null,
  };
  const optional: CandidateOption = {
    rank: 0,
    label: 'optional',
    constraint: { presence: 'optional' },
    support: { consistent: opportunities, outOf: opportunities },
    inductive: false,
    rationale: 'absence is always tolerated',
    distinguisher: null,
  };
  return { path: field.path, dimension: 'presence', options: rankOptions([required, optional]) };
}

function nullabilityDecision(field: FieldEvidence): DecisionPoint {
  const nonNull = field.present - field.nullCount;
  const nonNullOption: CandidateOption = {
    rank: 0,
    label: 'non-null',
    constraint: { nullable: false },
    support: { consistent: nonNull, outOf: field.present },
    inductive: field.nullCount === 0,
    rationale:
      field.nullCount === 0
        ? `null never observed in ${field.present} present sample(s) (sample-inductive)`
        : `null observed ${field.nullCount} time(s); contradicted by evidence`,
    distinguisher:
      field.nullCount === 0
        ? {
            instance: buildInstance(field.path, null),
            note: 'null is rejected by "non-null" but accepted by "nullable"',
          }
        : null,
  };
  const nullable: CandidateOption = {
    rank: 0,
    label: 'nullable',
    constraint: { nullable: true },
    support: { consistent: field.present, outOf: field.present },
    inductive: false,
    rationale: 'null is always tolerated',
    distinguisher: null,
  };
  return { path: field.path, dimension: 'nullability', options: rankOptions([nonNullOption, nullable]) };
}

function rangeDecision(field: FieldEvidence): DecisionPoint | null {
  if (!field.numericMin || !field.numericMax) return null;
  const numericObs = field.typeCounts.integer + field.typeCounts.number;
  const { value: min, sampleId: minBy } = field.numericMin;
  const { value: max, sampleId: maxBy } = field.numericMax;
  const bounded: CandidateOption = {
    rank: 0,
    label: `range [${min}, ${max}]`,
    constraint: { minimum: min, maximum: max },
    support: { consistent: numericObs, outOf: numericObs },
    inductive: true,
    rationale: `bounds contributed by sample "${minBy}" (min) and "${maxBy}" (max); retracting them shrinks the range; out-of-range values remain possible`,
    distinguisher: {
      instance: buildInstance(field.path, max + 1),
      note: `${max + 1} exceeds the observed max: rejected by the bounded option, accepted by "unbounded"`,
    },
  };
  const unbounded: CandidateOption = {
    rank: 0,
    label: 'unbounded',
    constraint: {},
    support: { consistent: numericObs, outOf: numericObs },
    inductive: false,
    rationale: 'no numeric bounds; samples cannot rule out values outside the observed interval',
    distinguisher: null,
  };
  return { path: field.path, dimension: 'range', options: rankOptions([bounded, unbounded]) };
}

function lengthDecision(field: FieldEvidence): DecisionPoint | null {
  if (field.minLength === null || field.maxLength === null) return null;
  const stringObs = field.typeCounts.string;
  const bounded: CandidateOption = {
    rank: 0,
    label: `length [${field.minLength}, ${field.maxLength}]`,
    constraint: { minLength: field.minLength, maxLength: field.maxLength },
    support: { consistent: stringObs, outOf: stringObs },
    inductive: true,
    rationale: 'string lengths are sample-inductive; longer or shorter strings remain possible',
    distinguisher: {
      instance: buildInstance(field.path, 'x'.repeat(field.maxLength + 1)),
      note: `a string of length ${field.maxLength + 1} exceeds the observed max: rejected by the bounded option, accepted by "unbounded"`,
    },
  };
  const unbounded: CandidateOption = {
    rank: 0,
    label: 'unbounded',
    constraint: {},
    support: { consistent: stringObs, outOf: stringObs },
    inductive: false,
    rationale: 'no length bounds',
    distinguisher: null,
  };
  return { path: field.path, dimension: 'length', options: rankOptions([bounded, unbounded]) };
}

interface TreeNode {
  children: Map<string, TreeNode>;
}

function buildPrimary(fields: Map<string, FieldEvidence>, decisionPoints: DecisionPoint[]): Schema {
  const root: TreeNode = { children: new Map() };
  for (const path of fields.keys()) {
    if (path === '') continue;
    const rawSegments = path.startsWith('/') ? path.slice(1).split('/') : [path];
    const segments: string[] = [];
    for (const raw of rawSegments) {
      if (raw.endsWith('[]')) {
        const base = raw.slice(0, -2);
        if (base !== '') segments.push(base);
        segments.push('[]');
      } else {
        segments.push(raw);
      }
    }
    let node = root;
    for (const seg of segments) {
      let next = node.children.get(seg);
      if (!next) {
        next = { children: new Map() };
        node.children.set(seg, next);
      }
      node = next;
    }
  }

  const topOption = new Map<string, CandidateOption>();
  for (const dp of decisionPoints) {
    topOption.set(dp.path + '|' + dp.dimension, dp.options[0]);
  }

  function toSchema(path: string, node: TreeNode): Schema {
    const schema: Schema = {};
    const typeConstraint = topOption.get(path + '|type')?.constraint as { type?: string | string[] } | undefined;
    if (typeConstraint?.type !== undefined) schema.type = typeConstraint.type;
    const rangeConstraint = topOption.get(path + '|range')?.constraint;
    if (rangeConstraint) Object.assign(schema, rangeConstraint);
    const lengthConstraint = topOption.get(path + '|length')?.constraint;
    if (lengthConstraint) Object.assign(schema, lengthConstraint);
    const nullability = topOption.get(path + '|nullability')?.constraint as { nullable?: boolean } | undefined;
    if (nullability?.nullable === true && schema.type !== undefined) {
      if (typeof schema.type === 'string') schema.type = [schema.type, 'null'];
      else if (!schema.type.includes('null')) schema.type = [...schema.type, 'null'];
    }
    const properties: Record<string, Schema> = {};
    const required: string[] = [];
    for (const [seg, child] of node.children) {
      const childPath = seg === '[]' ? path + '[]' : path === '' ? '/' + seg : path + '/' + seg;
      if (seg.endsWith('[]')) {
        schema.items = toSchema(childPath, child);
      } else {
        const key = unescapeSegment(seg);
        properties[key] = toSchema(childPath, child);
        const presence = topOption.get(childPath + '|presence')?.constraint as { presence?: string } | undefined;
        if (presence?.presence === 'required') required.push(key);
      }
    }
    if (Object.keys(properties).length > 0) schema.properties = properties;
    if (required.length > 0) schema.required = required;
    return schema;
  }

  return toSchema('', root);
}

export function inferReport(sampleSet: SampleSet): InferenceReport {
  const sampleCount = sampleSet.size;
  const { fields } = collectEvidence(sampleSet.entries());

  const decisionPoints: DecisionPoint[] = [];
  for (const field of fields.values()) {
    decisionPoints.push(typeDecision(field));
    if (field.path !== '' && !field.path.endsWith('[]')) decisionPoints.push(presenceDecision(field));
    if (field.path !== '') decisionPoints.push(nullabilityDecision(field));
    const range = rangeDecision(field);
    if (range) decisionPoints.push(range);
    const length = lengthDecision(field);
    if (length) decisionPoints.push(length);
  }

  const primary = buildPrimary(fields, decisionPoints);

  const unknowns: string[] = [];
  if (sampleCount === 0) {
    unknowns.push('no samples: every domain is unknown; the primary schema imposes no constraint');
  }
  for (const dp of decisionPoints) {
    if (dp.dimension === 'type' && dp.options[0].label === 'unknown') {
      unknowns.push(`${displayPath(dp.path)}: domain unknown (only null observed)`);
    }
  }
  for (const [path, field] of fields) {
    if (field.typeCounts.array > 0 && !fields.has(path + '[]')) {
      unknowns.push(`${displayPath(path)}[]: item domain unknown (no array elements observed)`);
    }
  }

  const errors: ValidationError[] = [];
  let accepted = 0;
  for (const [id, sample] of sampleSet.entries()) {
    const sampleErrors = validate(sample, primary);
    if (sampleErrors.length === 0) {
      accepted += 1;
    } else {
      errors.push(
        ...sampleErrors.map((e) => ({ ...e, message: `[sample "${id}"] ${e.message}` })),
      );
    }
  }

  const guarantees: string[] = [
    `open-world: object keys never observed are accepted; no additionalProperties ban is inferred or implemented`,
    `implemented subset only: ${IMPLEMENTED_KEYWORDS.join(', ')}; nothing beyond this subset is claimed`,
  ];
  if (sampleCount > 0) {
    guarantees.unshift(
      `verified: all ${accepted} retained sample(s) are accepted by the primary schema (independently re-validated, ${sampleCount - accepted} rejected)`,
    );
  }

  const inductions: string[] = [];
  for (const dp of decisionPoints) {
    const top = dp.options[0];
    if (top.inductive) {
      inductions.push(`${displayPath(dp.path)} [${dp.dimension}] ${top.label}: ${top.rationale}`);
    }
  }

  return {
    status: sampleCount === 0 ? 'unknown' : 'inferred',
    sampleCount,
    retainedSampleIds: sampleSet.ids(),
    decisionPoints,
    unknowns,
    primary,
    verification: { accepted, rejected: sampleCount - accepted, errors },
    guarantees,
    inductions,
  };
}

export class SchemaInference {
  private readonly samples = new SampleSet();

  addSample(id: string, value: Json): void {
    this.samples.add(id, value);
  }

  retractSample(id: string): boolean {
    return this.samples.remove(id);
  }

  report(): InferenceReport {
    return inferReport(this.samples);
  }
}
