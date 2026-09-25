import type {
  CollectResult,
  ExpandOptions,
  JsonValue,
  ParamSpec,
} from "./types.ts";
import { ExpandError } from "./errors.ts";
import { canonicalize, digestOf } from "./canonical.ts";
import {
  normalizeParams,
  validateIdentity,
  validateTemplate,
  type NormalizedSpec,
} from "./spec.ts";
import { compileConstraints, prefixFeasible, type CompiledConstraint } from "./constraints.ts";

const CURSOR_VERSION = 1;

interface CursorPayload {
  v: number;
  /** 输入摘要：绑定模板 + 参数 + 身份声明的版本。 */
  digest: string;
  /** 下一条已提交记录之后的里程表位置；未探索分支由此保留。 */
  pos: number[] | null;
  /** 已累计产出（去重后）的记录数。 */
  emitted: number;
  /** 已见过的身份键（规范化形式），保证跨游标去重。 */
  seen: string[];
}

interface Prepared {
  spec: NormalizedSpec;
  compiled: CompiledConstraint[];
  axisIndex: Map<string, number>;
  identity: string[];
  digest: string;
}

function prepare(
  template: JsonValue,
  params: ParamSpec,
  identity: string[]
): Prepared {
  const spec = normalizeParams(params);
  validateTemplate(template, new Set(spec.axisNames));
  const compiled = compileConstraints(spec.constraints, spec.axisNames);
  const axisIndex = new Map(spec.axisNames.map((n, i) => [n, i]));
  const canonicalParams: JsonValue = {
    axes: Object.fromEntries(spec.axisNames.map((name, i) => [name, spec.domains[i]!])),
    constraints: spec.constraints as unknown as JsonValue,
  };
  const digest = digestOf({ template, params: canonicalParams, identity });
  return { spec, compiled, axisIndex, identity, digest };
}

/** 里程表步进：level 处进位，更深的轴清零；越过最左轴返回 null。 */
function advance(pos: number[], level: number, sizes: number[]): number[] | null {
  if (level < 0) return null;
  const next = pos.slice();
  next[level] = next[level]! + 1;
  for (let d = level + 1; d < sizes.length; d++) next[d] = 0;
  let l = level;
  while (next[l]! >= sizes[l]!) {
    next[l] = 0;
    l -= 1;
    if (l < 0) return null;
    next[l] = next[l]! + 1;
  }
  return next;
}

export interface ComboHit {
  /** 命中组合的里程表位置（供 $index 与调试使用）。 */
  pos: number[];
  combo: JsonValue[];
}

/**
 * 组合枚举器：按确定顺序（轴名字典序、值域数组序）逐个产出可行组合。
 * 约束传播在前缀不可行时整枝跳过，从不展开完整笛卡尔积。
 */
class ComboEnumerator {
  private pos: number[] | null;
  private readonly sizes: number[];
  private readonly spec: NormalizedSpec;
  private readonly compiled: CompiledConstraint[];

  constructor(spec: NormalizedSpec, compiled: CompiledConstraint[], startPos?: number[] | null) {
    this.spec = spec;
    this.compiled = compiled;
    this.sizes = spec.domains.map((d) => d.length);
    this.pos = startPos === undefined ? this.sizes.map(() => 0) : startPos;
  }

  /** 当前内部位置（用于游标快照）。 */
  position(): number[] | null {
    return this.pos === null ? null : this.pos.slice();
  }

  /** 下一个可行组合；耗尽返回 null。 */
  next(): ComboHit | null {
    const n = this.sizes.length;
    while (this.pos !== null) {
      const pos = this.pos;
      let badLevel = -1;
      for (let d = 0; d < n; d++) {
        if (pos[d]! >= this.sizes[d]! || !prefixFeasible(pos, d, this.spec.domains, this.compiled)) {
          badLevel = d;
          break;
        }
      }
      if (badLevel === -1) {
        const combo = pos.map((valueIndex, axis) => this.spec.domains[axis]![valueIndex]!);
        this.pos = advance(pos, n - 1, this.sizes);
        return { pos: pos.slice(), combo };
      }
      this.pos = advance(pos, badLevel, this.sizes);
    }
    return null;
  }
}

function expandTemplate(
  template: JsonValue,
  combo: JsonValue[],
  pos: number[],
  axisIndex: Map<string, number>
): JsonValue {
  if (Array.isArray(template)) {
    return template.map((item) => expandTemplate(item, combo, pos, axisIndex));
  }
  if (typeof template === "object" && template !== null) {
    if ("$ref" in template) {
      const axis = axisIndex.get(template["$ref"] as string)!;
      return combo[axis]!;
    }
    if ("$index" in template) {
      const axis = axisIndex.get(template["$index"] as string)!;
      return pos[axis]!;
    }
    const out: Record<string, JsonValue> = {};
    for (const key of Object.keys(template)) {
      out[key] = expandTemplate(template[key]!, combo, pos, axisIndex);
    }
    return out;
  }
  return template;
}

function extractPath(record: JsonValue, path: string, identityIndex: number): JsonValue {
  let node = record;
  for (const segment of path.split(".")) {
    if (Array.isArray(node)) {
      const i = Number(segment);
      if (!Number.isInteger(i) || i < 0 || i >= node.length) {
        throw new ExpandError(
          `$.options.identity[${identityIndex}]`,
          `path "${path}" does not exist in record (failed at segment "${segment}")`
        );
      }
      node = node[i]!;
    } else if (typeof node === "object" && node !== null && segment in node) {
      node = (node as Record<string, JsonValue>)[segment]!;
    } else {
      throw new ExpandError(
        `$.options.identity[${identityIndex}]`,
        `path "${path}" does not exist in record (failed at segment "${segment}")`
      );
    }
  }
  return node;
}

interface PendingRecord {
  record: JsonValue;
  key: string | null;
  posAfter: number[] | null;
}

/**
 * 流式展开器：按需逐条产出，支持去重、窥视与游标快照；绝不先展开完整积。
 * 快照只包含已提交状态，窥视未提交的记录不会在续接时丢失。
 */
class RecordStream {
  private readonly enumerator: ComboEnumerator;
  private readonly seen = new Set<string>();
  private emittedCount = 0;
  private committedPos: number[] | null;
  private pending: PendingRecord | null = null;
  private readonly prepared: Prepared;
  private readonly template: JsonValue;

  constructor(prepared: Prepared, template: JsonValue, cursor?: CursorPayload) {
    this.prepared = prepared;
    this.template = template;
    this.enumerator = new ComboEnumerator(
      prepared.spec,
      prepared.compiled,
      cursor ? cursor.pos : undefined
    );
    if (cursor) {
      for (const key of cursor.seen) this.seen.add(key);
      this.emittedCount = cursor.emitted;
    }
    this.committedPos = this.enumerator.position();
  }

  private computeNext(): PendingRecord | null {
    const { axisIndex, identity } = this.prepared;
    while (true) {
      const hit = this.enumerator.next();
      if (hit === null) return null;
      const record = expandTemplate(this.template, hit.combo, hit.pos, axisIndex);
      let key: string | null = null;
      if (identity.length > 0) {
        key = canonicalize(identity.map((p, i) => extractPath(record, p, i)));
        if (this.seen.has(key)) continue;
      }
      return { record, key, posAfter: this.enumerator.position() };
    }
  }

  private commit(pending: PendingRecord): void {
    if (pending.key !== null) this.seen.add(pending.key);
    this.emittedCount += 1;
    this.committedPos = pending.posAfter;
  }

  /** 下一条记录（已应用去重）；耗尽返回 null。 */
  next(): JsonValue | null {
    const pending = this.pending ?? this.computeNext();
    this.pending = null;
    if (pending === null) {
      this.committedPos = null;
      return null;
    }
    this.commit(pending);
    return pending.record;
  }

  /** 窥视下一条记录但不消费；快照不会包含它。 */
  peek(): JsonValue | null {
    if (this.pending === null) {
      this.pending = this.computeNext();
      if (this.pending === null) this.committedPos = null;
    }
    return this.pending === null ? null : this.pending.record;
  }

  get emitted(): number {
    return this.emittedCount;
  }

  snapshot(digest: string): CursorPayload {
    return {
      v: CURSOR_VERSION,
      digest,
      pos: this.committedPos === null ? null : this.committedPos.slice(),
      emitted: this.emittedCount,
      seen: [...this.seen],
    };
  }
}

function decodeCursor(raw: string, expectedDigest: string): CursorPayload {
  let parsed: unknown;
  try {
    parsed = JSON.parse(Buffer.from(raw, "base64").toString("utf8"));
  } catch {
    throw new ExpandError("$.options.cursor", "cursor is not valid base64-encoded JSON");
  }
  const c = parsed as Partial<CursorPayload>;
  if (
    typeof c !== "object" || c === null ||
    c.v !== CURSOR_VERSION ||
    typeof c.digest !== "string" ||
    !(Array.isArray(c.pos) || c.pos === null) ||
    typeof c.emitted !== "number" ||
    !Array.isArray(c.seen)
  ) {
    throw new ExpandError("$.options.cursor", "cursor payload is malformed or has unsupported version");
  }
  if (c.digest !== expectedDigest) {
    throw new ExpandError(
      "$.options.cursor",
      "cursor is bound to a different template/params/identity digest; parameter changes invalidate old expansions"
    );
  }
  return c as CursorPayload;
}

function encodeCursor(payload: CursorPayload): string {
  return Buffer.from(JSON.stringify(payload), "utf8").toString("base64");
}

function makeStream(
  template: JsonValue,
  params: ParamSpec,
  options: ExpandOptions = {}
): { stream: RecordStream; digest: string } {
  const identity = validateIdentity(options.identity);
  const prepared = prepare(template, params, identity);
  let cursor: CursorPayload | undefined;
  if (options.cursor !== undefined) {
    cursor = decodeCursor(options.cursor, prepared.digest);
  }
  return { stream: new RecordStream(prepared, template, cursor), digest: prepared.digest };
}

/**
 * 流式生成：逐条产出记录（应用声明身份去重），不展开完整积。
 * 可配合 for...of 按需消费，超大空间只消费前缀也不会被完整枚举。
 */
export function* generate(
  template: JsonValue,
  params: ParamSpec,
  options: ExpandOptions = {}
): Generator<JsonValue, void, undefined> {
  const { stream } = makeStream(template, params, options);
  while (true) {
    const record = stream.next();
    if (record === null) return;
    yield record;
  }
}

/**
 * 批量收集：受 budget 额度限制；额度耗尽返回可恢复游标（保留未探索分支），
 * 此时 total 为 null，不伪报总量；穷举完毕时 total 为精确值。
 */
export function collect(
  template: JsonValue,
  params: ParamSpec,
  options: ExpandOptions = {}
): CollectResult {
  const budget = options.budget;
  if (budget !== undefined && (!Number.isInteger(budget) || budget < 0)) {
    throw new ExpandError("$.options.budget", "budget must be a non-negative integer");
  }
  const { stream, digest } = makeStream(template, params, options);
  const records: JsonValue[] = [];
  while (budget === undefined || records.length < budget) {
    const record = stream.next();
    if (record === null) {
      return { records, done: true, emitted: stream.emitted, total: stream.emitted };
    }
    records.push(record);
  }
  if (stream.peek() === null) {
    return { records, done: true, emitted: stream.emitted, total: stream.emitted };
  }
  return {
    records,
    done: false,
    cursor: encodeCursor(stream.snapshot(digest)),
    emitted: stream.emitted,
    total: null,
  };
}

/**
 * 提前估计数量：在不物化任何记录的前提下，用同一套约束传播精确计数可行组合。
 * （若启用去重，实际产出条数可能更少。）
 */
export function countFeasible(params: ParamSpec): number {
  const spec = normalizeParams(params);
  const compiled = compileConstraints(spec.constraints, spec.axisNames);
  const enumerator = new ComboEnumerator(spec, compiled);
  let count = 0;
  while (enumerator.next() !== null) count += 1;
  return count;
}

/** 按排名定位：返回去重后产出序列中 0 基第 rank 条记录；越界返回 null。 */
export function atRank(
  template: JsonValue,
  params: ParamSpec,
  rank: number,
  options: ExpandOptions = {}
): JsonValue | null {
  if (!Number.isInteger(rank) || rank < 0) {
    throw new ExpandError("$.rank", "rank must be a non-negative integer");
  }
  const { stream } = makeStream(template, params, options);
  let current = 0;
  while (true) {
    const record = stream.next();
    if (record === null) return null;
    if (current === rank) return record;
    current += 1;
  }
}

/** 反向排名：返回给定记录在去重后产出序列中的 0 基排名；不存在返回 -1。 */
export function rankOf(
  template: JsonValue,
  params: ParamSpec,
  record: JsonValue,
  options: ExpandOptions = {}
): number {
  const target = canonicalize(record);
  const { stream } = makeStream(template, params, options);
  let current = 0;
  while (true) {
    const candidate = stream.next();
    if (candidate === null) return -1;
    if (canonicalize(candidate) === target) return current;
    current += 1;
  }
}
