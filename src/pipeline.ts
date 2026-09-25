import type { Evidence, JSONValue, Relation } from "./types.ts";
import { LineageStore, type LineageRecord } from "./lineage.ts";
import { trackSource } from "./relation.ts";

/**
 * 组合式流水线：每个阶段提交一个新版本并把
 * “输出字段 -> 原始路径证据集” 的多对多映射写入 LineageStore。
 * 算子输出可直接作为下一阶段输入，血缘随值自动组合。
 */
export class Pipeline {
  readonly store = new LineageStore();
  private readonly versions = new Map<number, Relation>();

  /** 注册来源数据集（JSON 对象数组），提交一个版本。 */
  source(name: string, data: JSONValue[]): Relation {
    return this.commit(`source:${name}`, trackSource(name, data));
  }

  /** 提交一个阶段：记录该关系每个输出字段的血缘，返回带版本句柄的关系。 */
  commit(stage: string, rel: Relation): Relation {
    const version = this.store.beginStage(stage);
    for (let i = 0; i < rel.rows.length; i++) {
      for (const [fieldName, t] of Object.entries(rel.rows[i]!)) {
        this.store.record(version, [i, fieldName], t.evidence);
      }
    }
    this.versions.set(version, rel);
    return rel;
  }

  /** 运行一个算子并提交：run("agg", aggregateRel(...))。 */
  run(stage: string, rel: Relation): Relation {
    return this.commit(stage, rel);
  }

  /** 按版本查询血缘；不传版本返回全部版本。 */
  lineage(version?: number): LineageRecord[] {
    return this.store.query(version);
  }

  /** 截至某版本（含）每个输出字段的最新血缘。 */
  asOf(version: number): LineageRecord[] {
    return this.store.asOf(version);
  }

  /** 反查某输出字段（如 "$[0].total"）贡献证据，可限定版本。 */
  explain(output: string, version?: number): Evidence[] {
    return this.store.explain(output, version);
  }
}
