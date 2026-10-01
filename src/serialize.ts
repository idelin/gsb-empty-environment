import type { Segment, ValidationError } from './types.ts';
import { ErrorDigest } from './digest.ts';

export const FORMAT = 'error-digest/1';

export type RunJson =
  | { start: number; end: number; idStart: number }
  | { start: number; end: number; ids: number[][] };

export type TemplateJson =
  | { kind: 'range'; prefix: Segment[]; suffix: Segment[]; runs: RunJson[] }
  | { kind: 'literal'; path: Segment[]; ids: number[] };

export interface GroupJson {
  schemaVersion: string;
  keyword: string;
  schemaPath: string;
  message: string;
  count: number;
  templates: TemplateJson[];
}

export interface DigestDocument {
  format: typeof FORMAT;
  count: number;
  groups: GroupJson[];
}

function computeRuns(values: ReadonlyMap<number, readonly number[]>): RunJson[] {
  const sorted = [...values.keys()].sort((a, b) => a - b);
  const runs: RunJson[] = [];
  let i = 0;
  while (i < sorted.length) {
    let j = i;
    while (j + 1 < sorted.length && sorted[j + 1] === sorted[j] + 1) j++;
    const start = sorted[i];
    const end = sorted[j];
    const perValue = sorted.slice(i, j + 1).map((v) => [...values.get(v)!]);
    const flat = perValue.flat();
    const compact =
      perValue.every((ids) => ids.length === 1) && flat.every((id, k) => id === flat[0] + k);
    runs.push(compact ? { start, end, idStart: flat[0] } : { start, end, ids: perValue });
    i = j + 1;
  }
  return runs;
}

/** Encodes the digest into a JSON-safe compressed document. */
export function encode(digest: ErrorDigest): DigestDocument {
  const groups: GroupJson[] = [];
  for (const g of digest.grouped()) {
    const templates: TemplateJson[] = [];
    const rangeTemplates = g.templates
      .map((t) => ({
        kind: 'range' as const,
        prefix: t.prefix,
        suffix: t.suffix,
        runs: computeRuns(t.values),
      }))
      .sort((a, b) => JSON.stringify([a.prefix, a.suffix]).localeCompare(JSON.stringify([b.prefix, b.suffix])));
    const literalTemplates = g.literals
      .map((l) => ({ kind: 'literal' as const, path: l.path, ids: [...l.ids] }))
      .sort((a, b) => JSON.stringify(a.path).localeCompare(JSON.stringify(b.path)));
    templates.push(...rangeTemplates, ...literalTemplates);
    const runCount = (r: RunJson): number =>
      'idStart' in r ? r.end - r.start + 1 : r.ids.reduce((a2, ids) => a2 + ids.length, 0);
    const count =
      rangeTemplates.reduce((acc, t) => acc + t.runs.reduce((a2, r) => a2 + runCount(r), 0), 0) +
      literalTemplates.reduce((acc, t) => acc + t.ids.length, 0);
    groups.push({ ...g.fields, count, templates });
  }
  groups.sort((a, b) =>
    JSON.stringify([a.schemaVersion, a.keyword, a.schemaPath, a.message]).localeCompare(
      JSON.stringify([b.schemaVersion, b.keyword, b.schemaPath, b.message]),
    ),
  );
  return { format: FORMAT, count: groups.reduce((acc, g) => acc + g.count, 0), groups };
}

function fail(reason: string): never {
  throw new Error(`invalid digest document: ${reason}`);
}

const isObj = (x: unknown): x is Record<string, unknown> =>
  typeof x === 'object' && x !== null && !Array.isArray(x);

const isNonNegInt = (x: unknown): x is number =>
  typeof x === 'number' && Number.isInteger(x) && x >= 0;

const isSegment = (x: unknown): x is Segment => typeof x === 'string' || isNonNegInt(x);

function checkSegmentArray(x: unknown, what: string): Segment[] {
  if (!Array.isArray(x)) fail(`${what} must be an array`);
  for (const seg of x) {
    if (!isSegment(seg)) fail(`${what} contains an invalid path segment`);
  }
  return x as Segment[];
}

function checkIdArray(x: unknown, what: string): number[] {
  if (!Array.isArray(x) || x.length === 0) fail(`${what} must be a non-empty array of ids`);
  for (const id of x) {
    if (!isNonNegInt(id)) fail(`${what} contains an invalid id`);
  }
  return x as number[];
}

/**
 * Decodes a compressed document back into the full error list (insertion
 * order). The document is validated strictly: counts must match the actual
 * ranges, ranges must be legal (0 <= start <= end, sorted, non-overlapping),
 * and every id must be globally unique. Any tampering is rejected.
 */
export function decode(doc: unknown): ValidationError[] {
  if (!isObj(doc)) fail('document must be an object');
  if (doc.format !== FORMAT) fail(`format must be "${FORMAT}"`);
  if (!isNonNegInt(doc.count)) fail('count must be a non-negative integer');
  if (!Array.isArray(doc.groups)) fail('groups must be an array');

  const seenIds = new Set<number>();
  const expanded: Array<{ id: number; error: ValidationError }> = [];
  let actualTotal = 0;

  const claimId = (id: number): void => {
    if (seenIds.has(id)) fail(`duplicate id ${id}`);
    seenIds.add(id);
  };

  for (const g of doc.groups) {
    if (!isObj(g)) fail('group must be an object');
    for (const field of ['schemaVersion', 'keyword', 'schemaPath', 'message'] as const) {
      if (typeof g[field] !== 'string') fail(`group.${field} must be a string`);
    }
    if (!isNonNegInt(g.count)) fail('group.count must be a non-negative integer');
    if (!Array.isArray(g.templates)) fail('group.templates must be an array');
    const fields = {
      schemaVersion: g.schemaVersion as string,
      keyword: g.keyword as string,
      schemaPath: g.schemaPath as string,
      message: g.message as string,
    };
    let groupCount = 0;

    for (const t of g.templates) {
      if (!isObj(t)) fail('template must be an object');
      if (t.kind === 'range') {
        const prefix = checkSegmentArray(t.prefix, 'template.prefix');
        const suffix = checkSegmentArray(t.suffix, 'template.suffix');
        if (!Array.isArray(t.runs)) fail('template.runs must be an array');
        let prevEnd = -1;
        for (const r of t.runs) {
          if (!isObj(r)) fail('run must be an object');
          if (!isNonNegInt(r.start) || !isNonNegInt(r.end)) fail('run bounds must be non-negative integers');
          if (r.start > r.end) fail(`illegal range [${r.start}, ${r.end}]`);
          if (r.start <= prevEnd) fail('runs must be sorted and non-overlapping');
          prevEnd = r.end;
          const span = r.end - r.start + 1;
          let perValue: number[][];
          if (r.idStart !== undefined) {
            if (!isNonNegInt(r.idStart)) fail('run.idStart must be a non-negative integer');
            perValue = Array.from({ length: span }, (_, k) => [r.idStart as number + k]);
          } else if (r.ids !== undefined) {
            if (!Array.isArray(r.ids) || r.ids.length !== span) {
              fail(`run.ids must have exactly ${span} entries`);
            }
            perValue = r.ids.map((ids, k) => checkIdArray(ids, `run.ids[${k}]`));
          } else {
            fail('run must carry idStart or ids');
          }
          for (let k = 0; k < span; k++) {
            const path: Segment[] = [...prefix, r.start + k, ...suffix];
            for (const id of perValue[k]) {
              claimId(id);
              expanded.push({ id, error: { ...fields, instancePath: path } });
              groupCount++;
            }
          }
        }
      } else if (t.kind === 'literal') {
        const path = checkSegmentArray(t.path, 'template.path');
        const ids = checkIdArray(t.ids, 'template.ids');
        for (const id of ids) {
          claimId(id);
          expanded.push({ id, error: { ...fields, instancePath: [...path] } });
          groupCount++;
        }
      } else {
        fail('template.kind must be "range" or "literal"');
      }
    }

    if (groupCount !== g.count) {
      fail(`group.count ${g.count} does not match actual ${groupCount}`);
    }
    actualTotal += groupCount;
  }

  if (actualTotal !== doc.count) {
    fail(`count ${doc.count} does not match actual ${actualTotal}`);
  }

  expanded.sort((a, b) => a.id - b.id);
  return expanded.map((e) => e.error);
}
