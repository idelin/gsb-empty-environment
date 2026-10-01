import { createHash } from "node:crypto";
import {
  parsePath,
  pathToString,
  type PathSegment,
  type ValidationError,
} from "./errors.ts";
import {
  addIndex,
  countIndex,
  normalizeRanges,
  rangeCount,
  removeIndex,
  type Range,
} from "./ranges.ts";

export const FORMAT = "compressed-error-index/1";

type TemplateSegment = string | number | null;

interface GroupState {
  schemaVersion: string;
  rule: string;
  schemaPath: string;
  template: TemplateSegment[];
  slot: number; // position of the compressed array-index segment, -1 if none
  ranges: Range[];
  count: number;
  firstSeq: number;
  message: string;
  params?: Record<string, unknown>;
}

export interface SerializedGroup {
  schemaVersion: string;
  rule: string;
  schemaPath: string;
  template: TemplateSegment[];
  slot: number;
  ranges: [number, number, number][];
  count: number;
  firstSeq: number;
  message: string;
  params?: Record<string, unknown>;
}

export interface SerializedIndex {
  format: typeof FORMAT;
  totalCount: number;
  groups: SerializedGroup[];
  checksum: string;
}

export interface ExpandFilter {
  rule?: string;
  schemaVersion?: string;
  pathPrefix?: string | PathSegment[];
  order?: "original" | "path";
}

function templatize(path: readonly PathSegment[]): {
  template: TemplateSegment[];
  slot: number;
  index: number;
} {
  for (let i = path.length - 1; i >= 0; i--) {
    if (typeof path[i] === "number") {
      const template: TemplateSegment[] = path.slice();
      template[i] = null;
      return { template, slot: i, index: path[i] as number };
    }
  }
  return { template: path.slice(), slot: -1, index: 0 };
}

function groupKeyOf(
  err: ValidationError,
  template: readonly TemplateSegment[],
): string {
  return JSON.stringify([
    err.schemaVersion,
    err.rule,
    err.schemaPath,
    template,
    err.message,
    err.params ?? null,
  ]);
}

function matchPrefix(
  template: readonly TemplateSegment[],
  slot: number,
  prefix: readonly PathSegment[],
): { ok: boolean; pinnedIndex?: number } {
  if (prefix.length > template.length) return { ok: false };
  let pinnedIndex: number | undefined;
  for (let i = 0; i < prefix.length; i++) {
    if (i === slot) {
      const seg = prefix[i];
      if (typeof seg !== "number") return { ok: false };
      pinnedIndex = seg;
    } else if (template[i] !== prefix[i]) {
      return { ok: false };
    }
  }
  return pinnedIndex === undefined ? { ok: true } : { ok: true, pinnedIndex };
}

function stableStringify(value: unknown): string {
  if (value === null || typeof value !== "object") return JSON.stringify(value) as string;
  if (Array.isArray(value)) {
    return "[" + value.map((v) => stableStringify(v)).join(",") + "]";
  }
  const obj = value as Record<string, unknown>;
  const keys = Object.keys(obj).sort();
  return (
    "{" +
    keys.map((k) => JSON.stringify(k) + ":" + stableStringify(obj[k])).join(",") +
    "}"
  );
}

export function computeChecksum(payload: {
  format: string;
  totalCount: number;
  groups: SerializedGroup[];
}): string {
  return createHash("sha256").update(stableStringify(payload)).digest("hex");
}

export class ErrorIndex {
  private groups = new Map<string, GroupState>();
  private seqCounter = 0;
  private total = 0;

  static fromErrors(errors: readonly ValidationError[]): ErrorIndex {
    const index = new ErrorIndex();
    index.addAll(errors);
    return index;
  }

  get totalCount(): number {
    return this.total;
  }

  get groupCount(): number {
    return this.groups.size;
  }

  addError(err: ValidationError): void {
    const { template, slot, index } = templatize(err.instancePath);
    const key = groupKeyOf(err, template);
    let group = this.groups.get(key);
    if (!group) {
      group = {
        schemaVersion: err.schemaVersion,
        rule: err.rule,
        schemaPath: err.schemaPath,
        template,
        slot,
        ranges: [],
        count: 0,
        firstSeq: this.seqCounter,
        message: err.message,
        ...(err.params !== undefined
          ? { params: structuredClone(err.params) }
          : {}),
      };
      this.groups.set(key, group);
    }
    this.seqCounter++;
    group.ranges = addIndex(group.ranges, index);
    group.count++;
    this.total++;
  }

  addAll(errors: readonly ValidationError[]): void {
    for (const err of errors) this.addError(err);
  }

  /** Remove a single occurrence of an identical error. Returns false if absent. */
  retractError(err: ValidationError): boolean {
    const { template, index } = templatize(err.instancePath);
    const key = groupKeyOf(err, template);
    const group = this.groups.get(key);
    if (!group) return false;
    const next = removeIndex(group.ranges, index);
    if (next === null) return false;
    group.ranges = next;
    group.count--;
    this.total--;
    if (group.count === 0) this.groups.delete(key);
    return true;
  }

  private matchingGroups(filter?: ExpandFilter): {
    group: GroupState;
    pinnedIndex?: number;
  }[] {
    const prefix =
      filter?.pathPrefix === undefined
        ? undefined
        : typeof filter.pathPrefix === "string"
          ? parsePath(filter.pathPrefix)
          : filter.pathPrefix;
    const out: { group: GroupState; pinnedIndex?: number }[] = [];
    for (const group of this.groups.values()) {
      if (filter?.rule !== undefined && group.rule !== filter.rule) continue;
      if (
        filter?.schemaVersion !== undefined &&
        group.schemaVersion !== filter.schemaVersion
      )
        continue;
      let pinnedIndex: number | undefined;
      if (prefix !== undefined) {
        const match = matchPrefix(group.template, group.slot, prefix);
        if (!match.ok) continue;
        pinnedIndex = match.pinnedIndex;
      }
      out.push({ group, pinnedIndex });
    }
    out.sort((a, b) => a.group.firstSeq - b.group.firstSeq);
    return out;
  }

  expand(filter?: ExpandFilter): ValidationError[] {
    const matched = this.matchingGroups(filter);
    const out: ValidationError[] = [];
    for (const { group, pinnedIndex } of matched) {
      for (const [start, end, mult] of group.ranges) {
        for (let index = start; index <= end; index++) {
          if (pinnedIndex !== undefined && index !== pinnedIndex) continue;
          for (let k = 0; k < mult; k++) {
            const path = group.template.slice() as PathSegment[];
            if (group.slot >= 0) path[group.slot] = index;
            out.push({
              schemaVersion: group.schemaVersion,
              rule: group.rule,
              schemaPath: group.schemaPath,
              instancePath: path,
              message: group.message,
              ...(group.params !== undefined
                ? { params: structuredClone(group.params) }
                : {}),
            });
          }
        }
      }
    }
    if (filter?.order === "path") {
      out.sort((a, b) => {
        const pa = pathToString(a.instancePath);
        const pb = pathToString(b.instancePath);
        return pa < pb ? -1 : pa > pb ? 1 : 0;
      });
    }
    return out;
  }

  count(filter?: ExpandFilter): number {
    if (filter === undefined) return this.total;
    let total = 0;
    for (const { group, pinnedIndex } of this.matchingGroups(filter)) {
      total +=
        pinnedIndex === undefined
          ? group.count
          : countIndex(group.ranges, pinnedIndex);
    }
    return total;
  }

  serialize(): SerializedIndex {
    const groups: SerializedGroup[] = [...this.groups.values()]
      .sort((a, b) => a.firstSeq - b.firstSeq)
      .map((g) => ({
        schemaVersion: g.schemaVersion,
        rule: g.rule,
        schemaPath: g.schemaPath,
        template: g.template.slice(),
        slot: g.slot,
        ranges: g.ranges.map(([s, e, m]): [number, number, number] => [s, e, m]),
        count: g.count,
        firstSeq: g.firstSeq,
        message: g.message,
        ...(g.params !== undefined ? { params: structuredClone(g.params) } : {}),
      }));
    const payload = { format: FORMAT, totalCount: this.total, groups };
    return { ...payload, checksum: computeChecksum(payload) };
  }

  toJSONString(): string {
    return JSON.stringify(this.serialize());
  }

  static deserialize(raw: unknown): ErrorIndex {
    if (typeof raw === "string") raw = JSON.parse(raw);
    if (typeof raw !== "object" || raw === null) {
      throw new Error("invalid payload: not an object");
    }
    const data = raw as Record<string, unknown>;
    if (data.format !== FORMAT) throw new Error(`unsupported format: ${String(data.format)}`);
    if (typeof data.totalCount !== "number" || !Number.isInteger(data.totalCount) || data.totalCount < 0) {
      throw new Error("invalid payload: bad totalCount");
    }
    if (!Array.isArray(data.groups)) throw new Error("invalid payload: groups must be an array");
    if (typeof data.checksum !== "string") throw new Error("invalid payload: missing checksum");

    const payload = {
      format: FORMAT,
      totalCount: data.totalCount,
      groups: data.groups,
    };
    if (computeChecksum(payload as never) !== data.checksum) {
      throw new Error("checksum mismatch: payload was tampered with");
    }

    const index = new ErrorIndex();
    let sum = 0;
    let maxSeq = -1;
    for (const rawGroup of data.groups as Record<string, unknown>[]) {
      const group = validateGroup(rawGroup);
      sum += group.count;
      maxSeq = Math.max(maxSeq, group.firstSeq);
      const key = groupKeyOf(
        {
          schemaVersion: group.schemaVersion,
          rule: group.rule,
          schemaPath: group.schemaPath,
          instancePath: [],
          message: group.message,
          ...(group.params !== undefined ? { params: group.params } : {}),
        },
        group.template,
      );
      if (index.groups.has(key)) {
        throw new Error("invalid payload: duplicate group");
      }
      index.groups.set(key, group);
    }
    if (sum !== data.totalCount) {
      throw new Error(`count mismatch: groups sum to ${sum}, totalCount says ${data.totalCount}`);
    }
    index.total = data.totalCount;
    index.seqCounter = maxSeq + 1;
    return index;
  }
}

function isNonNegativeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= 0;
}

function validateGroup(raw: Record<string, unknown>): GroupState {
  for (const field of ["schemaVersion", "rule", "schemaPath", "message"] as const) {
    if (typeof raw[field] !== "string") {
      throw new Error(`invalid group: ${field} must be a string`);
    }
  }
  if (!Array.isArray(raw.template)) throw new Error("invalid group: template must be an array");
  const template = raw.template as TemplateSegment[];
  for (const seg of template) {
    if (seg !== null && typeof seg !== "string" && typeof seg !== "number") {
      throw new Error("invalid group: bad template segment");
    }
  }
  const slot = raw.slot;
  if (
    typeof slot !== "number" ||
    !Number.isInteger(slot) ||
    slot < -1 ||
    slot >= template.length
  ) {
    throw new Error("invalid group: slot out of bounds");
  }
  const nullSlots = template
    .map((seg, i) => (seg === null ? i : -1))
    .filter((i) => i >= 0);
  if (slot === -1) {
    if (nullSlots.length !== 0) throw new Error("invalid group: null segment without slot");
  } else {
    if (nullSlots.length !== 1 || nullSlots[0] !== slot) {
      throw new Error("invalid group: template must contain exactly one null at slot");
    }
  }
  if (!Array.isArray(raw.ranges)) throw new Error("invalid group: ranges must be an array");
  const ranges: Range[] = (raw.ranges as unknown[]).map((r) => {
    if (!Array.isArray(r) || r.length !== 3) throw new Error("invalid range: must be [start, end, mult]");
    const [start, end, mult] = r as unknown[];
    if (!isNonNegativeInteger(start) || !isNonNegativeInteger(end) || !isNonNegativeInteger(mult)) {
      throw new Error("invalid range: start/end/mult must be non-negative integers");
    }
    if (mult < 1) throw new Error("invalid range: mult must be >= 1");
    if (start > end) throw new Error(`invalid range: start ${start} > end ${end}`);
    return [start, end, mult];
  });
  for (let i = 1; i < ranges.length; i++) {
    const [, prevEnd, prevMult] = ranges[i - 1];
    const [start, , mult] = ranges[i];
    if (start <= prevEnd) throw new Error("invalid ranges: overlapping or unsorted");
    if (start === prevEnd + 1 && mult === prevMult) {
      throw new Error("invalid ranges: adjacent ranges with equal mult must be merged");
    }
  }
  if (!isNonNegativeInteger(raw.count)) throw new Error("invalid group: bad count");
  const count = raw.count;
  const actual = rangeCount(ranges);
  if (count !== actual) {
    throw new Error(`invalid group: count ${count} does not match ranges (${actual})`);
  }
  if (!isNonNegativeInteger(raw.firstSeq)) throw new Error("invalid group: bad firstSeq");
  if (raw.params !== undefined && (typeof raw.params !== "object" || raw.params === null)) {
    throw new Error("invalid group: params must be an object");
  }
  return {
    schemaVersion: raw.schemaVersion as string,
    rule: raw.rule as string,
    schemaPath: raw.schemaPath as string,
    template,
    slot: slot as number,
    ranges: normalizeRanges(ranges),
    count,
    firstSeq: raw.firstSeq,
    message: raw.message as string,
    ...(raw.params !== undefined ? { params: raw.params as Record<string, unknown> } : {}),
  };
}

export interface SpaceSavings {
  rawBytes: number;
  compressedBytes: number;
  ratio: number;
  savedBytes: number;
}

export function measureSpaceSavings(
  errors: readonly ValidationError[],
  index: ErrorIndex,
): SpaceSavings {
  const rawBytes = JSON.stringify(errors).length;
  const compressedBytes = JSON.stringify(index.serialize()).length;
  return {
    rawBytes,
    compressedBytes,
    ratio: rawBytes === 0 ? 1 : compressedBytes / rawBytes,
    savedBytes: rawBytes - compressedBytes,
  };
}
