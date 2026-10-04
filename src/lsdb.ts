import { compareVersion, formatVersion, type Version } from "./version.ts";

/** 一条边更新；weight 为 null 表示删除该边（留下版本墓碑）。 */
export interface EdgeUpdate {
  src: string;
  dst: string;
  weight: number | null;
  version: Version;
}

export interface EdgeRecord {
  /** deleted 为 true 时无意义，保留仅为结构完整。 */
  weight: number;
  deleted: boolean;
  version: Version;
}

export interface EdgeChange {
  src: string;
  dst: string;
  before: EdgeRecord | null;
  after: EdgeRecord;
}

export interface BatchResult {
  /** 真正改变数据库的条目数。 */
  applied: number;
  /** 因版本过旧或内容重复而被忽略的条目数。 */
  ignored: number;
  changes: EdgeChange[];
}

/** 同版本但内容冲突（权重不同，或一边为删除一边为新增）。 */
export class ConflictError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ConflictError";
  }
}

export class InvalidUpdateError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "InvalidUpdateError";
  }
}

function sameContent(a: EdgeRecord, weight: number | null): boolean {
  const deleted = weight === null;
  if (a.deleted !== deleted) return false;
  return deleted || a.weight === weight;
}

function validate(u: EdgeUpdate): void {
  if (!u.src || !u.dst) throw new InvalidUpdateError("src/dst 不能为空");
  if (u.src === u.dst) throw new InvalidUpdateError(`不允许自环边 ${u.src}`);
  if (!Number.isInteger(u.version.epoch) || u.version.epoch < 0)
    throw new InvalidUpdateError(`非法 epoch: ${u.version.epoch}`);
  if (!Number.isInteger(u.version.seq) || u.version.seq < 0)
    throw new InvalidUpdateError(`非法 seq: ${u.version.seq}`);
  if (u.weight !== null && (!Number.isInteger(u.weight) || u.weight < 0))
    throw new InvalidUpdateError(`权重必须为非负整数: ${u.weight}`);
}

/**
 * 本地链路状态数据库（有向图，非负整数权重）。
 * 每条边以 (src, dst) 为键，保存源节点通告的版本 (epoch, seq)。
 * 删除保留墓碑，旧版本消息无法复活已删除的边。
 */
export class LinkStateDatabase {
  private edges = new Map<string, Map<string, EdgeRecord>>();

  get(src: string, dst: string): EdgeRecord | null {
    return this.edges.get(src)?.get(dst) ?? null;
  }

  /**
   * 原子地应用一个更新批次：任一条目非法或冲突时整体回滚，
   * 对外要么全部可见，要么全部不可见。
   */
  applyBatch(updates: EdgeUpdate[]): BatchResult {
    for (const u of updates) validate(u);

    // 在暂存副本上顺序应用，全部成功后才提交，保证原子性。
    const staged = new Map<string, Map<string, EdgeRecord>>();
    const rowFor = (src: string): Map<string, EdgeRecord> => {
      let row = staged.get(src);
      if (!row) {
        row = new Map(this.edges.get(src) ?? []);
        staged.set(src, row);
      }
      return row;
    };

    const changes: EdgeChange[] = [];
    let ignored = 0;

    for (const u of updates) {
      const row = rowFor(u.src);
      const cur = row.get(u.dst) ?? null;
      if (cur) {
        const cmp = compareVersion(u.version, cur.version);
        if (cmp < 0) {
          ignored++; // 旧消息（包括试图复活墓碑的旧新增）直接忽略
          continue;
        }
        if (cmp === 0) {
          if (sameContent(cur, u.weight)) {
            ignored++; // 重复版本内容相同：幂等去重
            continue;
          }
          throw new ConflictError(
            `边 ${u.src}->${u.dst} 版本 ${formatVersion(u.version)} 内容冲突: ` +
              `已存 ${cur.deleted ? "删除" : `weight=${cur.weight}`}, ` +
              `收到 ${u.weight === null ? "删除" : `weight=${u.weight}`}`,
          );
        }
      }
      const after: EdgeRecord = {
        weight: u.weight ?? 0,
        deleted: u.weight === null,
        version: { ...u.version },
      };
      row.set(u.dst, after);
      changes.push({ src: u.src, dst: u.dst, before: cur, after });
    }

    for (const [src, row] of staged) this.edges.set(src, row);
    return { applied: changes.length, ignored, changes };
  }

  /** 所有已知节点（含仅有墓碑的节点）。 */
  nodes(): string[] {
    const set = new Set<string>();
    for (const [src, row] of this.edges) {
      set.add(src);
      for (const dst of row.keys()) set.add(dst);
    }
    return [...set];
  }

  /** 当前生效（未删除）的邻接表快照。 */
  adjacency(): Map<string, Map<string, { weight: number; version: Version }>> {
    const adj = new Map<string, Map<string, { weight: number; version: Version }>>();
    for (const [src, row] of this.edges) {
      for (const [dst, rec] of row) {
        if (rec.deleted) continue;
        let out = adj.get(src);
        if (!out) adj.set(src, (out = new Map()));
        out.set(dst, { weight: rec.weight, version: { ...rec.version } });
      }
    }
    return adj;
  }
}
