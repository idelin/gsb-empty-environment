import type { Segment, ValidationError } from './types.ts';

export interface GroupKeyFields {
  schemaVersion: string;
  keyword: string;
  schemaPath: string;
  message: string;
}

export interface GroupSummary extends GroupKeyFields {
  count: number;
}

export interface TemplateView {
  prefix: Segment[];
  suffix: Segment[];
  values: ReadonlyMap<number, readonly number[]>;
}

export interface LiteralView {
  path: Segment[];
  ids: readonly number[];
}

export interface GroupView {
  fields: GroupKeyFields;
  templates: TemplateView[];
  literals: LiteralView[];
}

interface Template {
  prefix: Segment[];
  suffix: Segment[];
  values: Map<number, number[]>;
}

interface Group {
  fields: GroupKeyFields;
  templates: Map<string, Template>;
  literals: Map<string, { path: Segment[]; ids: number[] }>;
  count: number;
}

type Ref =
  | { kind: 'range'; groupKey: string; templateKey: string; value: number }
  | { kind: 'literal'; groupKey: string; literalKey: string };

export type ExpandOrder = 'insertion' | 'path';

const groupKeyOf = (f: GroupKeyFields): string =>
  JSON.stringify([f.schemaVersion, f.keyword, f.schemaPath, f.message]);

const templateKeyOf = (prefix: Segment[], suffix: Segment[]): string =>
  JSON.stringify([prefix, suffix]);

function compareSegments(a: Segment[], b: Segment[]): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    const x = a[i];
    const y = b[i];
    if (x === y) continue;
    if (typeof x === 'number' && typeof y === 'number') return x - y;
    if (typeof x === 'number') return -1;
    if (typeof y === 'number') return 1;
    return String(x) < String(y) ? -1 : 1;
  }
  return a.length - b.length;
}

function startsWithSegment(path: Segment[], prefix: Segment[]): boolean {
  if (prefix.length > path.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    if (path[i] !== prefix[i]) return false;
  }
  return true;
}

/**
 * Incremental index over validation errors.
 *
 * Errors are grouped by (schemaVersion, keyword, schemaPath, message) so
 * different rule reasons are never merged. Inside a group, instance paths
 * whose last numeric segment forms a consecutive run share one template,
 * which is what makes array-index ranges compressible. add/remove touch
 * only the affected group and template; nothing is rebuilt globally.
 */
export class ErrorDigest {
  private groups = new Map<string, Group>();
  private refs = new Map<number, Ref>();
  private nextId = 0;
  private total = 0;

  get count(): number {
    return this.total;
  }

  add(error: ValidationError): number {
    const id = this.nextId++;
    const fields: GroupKeyFields = {
      schemaVersion: error.schemaVersion,
      keyword: error.keyword,
      schemaPath: error.schemaPath,
      message: error.message,
    };
    const gk = groupKeyOf(fields);
    let group = this.groups.get(gk);
    if (group === undefined) {
      group = { fields, templates: new Map(), literals: new Map(), count: 0 };
      this.groups.set(gk, group);
    }
    const path = error.instancePath;
    let slot = -1;
    for (let i = path.length - 1; i >= 0; i--) {
      if (typeof path[i] === 'number') {
        slot = i;
        break;
      }
    }
    if (slot >= 0) {
      const value = path[slot] as number;
      const prefix = path.slice(0, slot);
      const suffix = path.slice(slot + 1);
      const tk = templateKeyOf(prefix, suffix);
      let t = group.templates.get(tk);
      if (t === undefined) {
        t = { prefix, suffix, values: new Map() };
        group.templates.set(tk, t);
      }
      const ids = t.values.get(value);
      if (ids !== undefined) ids.push(id);
      else t.values.set(value, [id]);
      this.refs.set(id, { kind: 'range', groupKey: gk, templateKey: tk, value });
    } else {
      const lk = JSON.stringify(path);
      let lit = group.literals.get(lk);
      if (lit === undefined) {
        lit = { path: [...path], ids: [] };
        group.literals.set(lk, lit);
      }
      lit.ids.push(id);
      this.refs.set(id, { kind: 'literal', groupKey: gk, literalKey: lk });
    }
    group.count++;
    this.total++;
    return id;
  }

  remove(id: number): boolean {
    const ref = this.refs.get(id);
    if (ref === undefined) return false;
    this.refs.delete(id);
    const group = this.groups.get(ref.groupKey);
    if (group === undefined) throw new Error('digest index corrupted: missing group');
    if (ref.kind === 'range') {
      const t = group.templates.get(ref.templateKey);
      if (t === undefined) throw new Error('digest index corrupted: missing template');
      const ids = t.values.get(ref.value);
      if (ids === undefined) throw new Error('digest index corrupted: missing value');
      ids.splice(ids.indexOf(id), 1);
      if (ids.length === 0) t.values.delete(ref.value);
      if (t.values.size === 0) group.templates.delete(ref.templateKey);
    } else {
      const lit = group.literals.get(ref.literalKey);
      if (lit === undefined) throw new Error('digest index corrupted: missing literal');
      lit.ids.splice(lit.ids.indexOf(id), 1);
      if (lit.ids.length === 0) group.literals.delete(ref.literalKey);
    }
    group.count--;
    this.total--;
    if (group.count === 0) this.groups.delete(ref.groupKey);
    return true;
  }

  summaries(): GroupSummary[] {
    return [...this.groups.values()].map((g) => ({ ...g.fields, count: g.count }));
  }

  expandAll(order: ExpandOrder = 'insertion'): ValidationError[] {
    return this.finish([...this.entries()], order);
  }

  expandByRule(keyword: string, schemaVersion?: string, order: ExpandOrder = 'insertion'): ValidationError[] {
    const picked = [...this.entries()].filter(
      (e) =>
        e.group.fields.keyword === keyword &&
        (schemaVersion === undefined || e.group.fields.schemaVersion === schemaVersion),
    );
    return this.finish(picked, order);
  }

  expandByPrefix(prefix: Segment[], order: ExpandOrder = 'insertion'): ValidationError[] {
    const picked = [...this.entries()].filter((e) => startsWithSegment(e.error.instancePath, prefix));
    return this.finish(picked, order);
  }

  /** Read-only structural view used by the serializer. */
  *grouped(): Generator<GroupView> {
    for (const g of this.groups.values()) {
      yield {
        fields: { ...g.fields },
        templates: [...g.templates.values()].map((t) => ({
          prefix: [...t.prefix],
          suffix: [...t.suffix],
          values: t.values,
        })),
        literals: [...g.literals.values()].map((l) => ({ path: [...l.path], ids: l.ids })),
      };
    }
  }

  private buildError(fields: GroupKeyFields, instancePath: Segment[]): ValidationError {
    return {
      schemaVersion: fields.schemaVersion,
      keyword: fields.keyword,
      schemaPath: fields.schemaPath,
      message: fields.message,
      instancePath,
    };
  }

  private *entries(): Generator<{ id: number; error: ValidationError; group: Group }> {
    for (const group of this.groups.values()) {
      for (const t of group.templates.values()) {
        for (const [value, ids] of t.values) {
          const path: Segment[] = [...t.prefix, value, ...t.suffix];
          for (const id of ids) {
            yield { id, error: this.buildError(group.fields, path), group };
          }
        }
      }
      for (const lit of group.literals.values()) {
        for (const id of lit.ids) {
          yield { id, error: this.buildError(group.fields, [...lit.path]), group };
        }
      }
    }
  }

  private finish(picked: Array<{ id: number; error: ValidationError }>, order: ExpandOrder): ValidationError[] {
    if (order === 'insertion') {
      picked.sort((a, b) => a.id - b.id);
    } else {
      picked.sort((a, b) => compareSegments(a.error.instancePath, b.error.instancePath) || a.id - b.id);
    }
    return picked.map((p) => p.error);
  }
}
