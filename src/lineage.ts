import type { Evidence, Path } from "./types.ts";
import { formatPath } from "./errors.ts";

/** 证据去重键：来源 + 路径。同值不同来源 => 不同键，绝不合并。 */
export function evidenceKey(e: Evidence): string {
  return `${e.source}${JSON.stringify(e.path)}`;
}

/** 合并证据列表并按 (source, path) 去重，保持首次出现顺序。 */
export function mergeEvidence(...lists: readonly (readonly Evidence[])[]): Evidence[] {
  const seen = new Set<string>();
  const out: Evidence[] = [];
  for (const list of lists) {
    for (const e of list) {
      const k = evidenceKey(e);
      if (!seen.has(k)) {
        seen.add(k);
        out.push(e);
      }
    }
  }
  return out;
}

/** 一条血缘记录：某版本某输出字段 <- 一组原始路径证据（多对多）。 */
export interface LineageRecord {
  readonly version: number;
  readonly stage: string;
  /** 输出路径，形如 $[0].total */
  readonly output: string;
  readonly evidence: Evidence[];
}

function recordKey(version: number, output: string, evidence: readonly Evidence[]): string {
  const ev = evidence.map(evidenceKey).sort().join("");
  return `${version}${output}${ev}`;
}

/**
 * 版本化血缘存储。
 * - 每个阶段（stage）提交一个递增版本；
 * - 记录按 (version, output, evidence-set) 去重；
 * - 支持按版本精确查询、asOf 累积查询、按输出路径反查证据。
 */
export class LineageStore {
  private readonly records = new Map<string, LineageRecord>();
  private readonly stageNames = new Map<number, string>();
  private seq = 0;

  /** 开启一个阶段，返回其版本号。 */
  beginStage(name: string): number {
    const version = ++this.seq;
    this.stageNames.set(version, name);
    return version;
  }

  /** 记录一条 输出路径 -> 证据集 的映射（自动去重）。 */
  record(version: number, outputPath: Path, evidence: readonly Evidence[]): void {
    const stage = this.stageNames.get(version);
    if (stage === undefined) throw new Error(`unknown version ${version}`);
    const output = formatPath(outputPath);
    const deduped = mergeEvidence(evidence);
    const rec: LineageRecord = { version, stage, output, evidence: deduped };
    this.records.set(recordKey(version, output, deduped), rec);
  }

  /** 精确查询某版本的全部血缘记录；不传版本则返回全部版本。 */
  query(version?: number): LineageRecord[] {
    const all = [...this.records.values()];
    const filtered = version === undefined ? all : all.filter((r) => r.version === version);
    return filtered.sort((a, b) => a.version - b.version || a.output.localeCompare(b.output));
  }

  /** 累积查询：截至某版本（含）每个输出路径的最新记录。 */
  asOf(version: number): LineageRecord[] {
    const latest = new Map<string, LineageRecord>();
    for (const r of this.records.values()) {
      if (r.version > version) continue;
      const prev = latest.get(r.output);
      if (prev === undefined || prev.version < r.version) latest.set(r.output, r);
    }
    return [...latest.values()].sort((a, b) => a.output.localeCompare(b.output));
  }

  /** 反查某输出路径的证据（合并去重）；可限定版本。 */
  explain(output: string, version?: number): Evidence[] {
    const lists = this.query(version)
      .filter((r) => r.output === output)
      .map((r) => r.evidence);
    return mergeEvidence(...lists);
  }

  get latestVersion(): number {
    return this.seq;
  }
}
