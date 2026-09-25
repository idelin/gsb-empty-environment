import type {
  FieldError,
  FieldRecord,
  IdempotencyReport,
  InverseOp,
  JsonValue,
  NormalizeResult,
  RuleSet,
  RuleSetAnalysis,
} from "./types.ts";
import { analyzeRuleset, searchIdempotency } from "./dependency.ts";
import { identityOf, normalize } from "./normalize.ts";
import { walkLeaves } from "./pointer.ts";

export interface PublishedVersion {
  version: string;
  ruleset: RuleSet;
  analysis: RuleSetAnalysis;
  idempotency: IdempotencyReport;
  publishedAt: string;
}

export interface RejectedPublish {
  version: string;
  analysis: RuleSetAnalysis;
  idempotency: IdempotencyReport | null;
  reasons: string[];
  rejectedAt: string;
}

export interface PublishResult {
  ok: boolean;
  promoted: boolean;
  version: string;
  reasons: string[];
  analysis: RuleSetAnalysis;
  idempotency: IdempotencyReport | null;
  /** 发布后仍然激活的版本（非法发布时保持上一版本不变）。 */
  activeVersion: string | null;
}

export interface PublishOptions {
  /** 幂等反例搜索预算（默认 20000）。 */
  budget?: number;
}

/**
 * 规则注册表：版本化发布。
 * 只有静态分析通过且幂等性在声明域上被证明（proved）的规则集才能被提升为激活版本；
 * 非法发布被拒绝并保留，上一激活版本的结果不受任何影响。
 */
export class RuleRegistry {
  private versions = new Map<string, PublishedVersion>();
  private rejected = new Map<string, RejectedPublish>();
  private active: string | null = null;

  publish(version: string, ruleset: RuleSet, options: PublishOptions = {}): PublishResult {
    const analysis = analyzeRuleset(ruleset);
    const reasons: string[] = [...analysis.errors];

    let idempotency: IdempotencyReport | null = null;
    if (analysis.ok) {
      idempotency = searchIdempotency(ruleset, { budget: options.budget ?? 20000 });
      if (idempotency.status === "refuted") {
        reasons.push(
          `幂等性被反例证伪，禁止发布:\\n${idempotency.counterexamples
            .map((c) => `    "${c.input}" -> "${c.once}" -> "${c.twice}"`)
            .join("\\n")}`,
        );
      } else if (idempotency.status === "undecided") {
        reasons.push(
          `幂等性搜索预算未决（${idempotency.evaluated}/${idempotency.domainSize}），未证明，禁止提升为激活版本`,
        );
      }
    }

    const ok = analysis.ok && idempotency?.status === "proved";
    if (ok) {
      this.versions.set(version, {
        version,
        ruleset,
        analysis,
        idempotency: idempotency!,
        publishedAt: new Date(0).toISOString(),
      });
      this.active = version;
      return {
        ok: true,
        promoted: true,
        version,
        reasons: [],
        analysis,
        idempotency,
        activeVersion: this.active,
      };
    }

    this.rejected.set(version, {
      version,
      analysis,
      idempotency,
      reasons,
      rejectedAt: new Date(0).toISOString(),
    });
    return {
      ok: false,
      promoted: false,
      version,
      reasons,
      analysis,
      idempotency,
      activeVersion: this.active,
    };
  }

  get activeVersion(): string | null {
    return this.active;
  }

  getActive(): PublishedVersion | null {
    return this.active ? (this.versions.get(this.active) ?? null) : null;
  }

  getVersion(version: string): PublishedVersion | null {
    return this.versions.get(version) ?? null;
  }

  getRejected(version: string): RejectedPublish | null {
    return this.rejected.get(version) ?? null;
  }

  listVersions(): string[] {
    return [...this.versions.keys()];
  }

  /** 回滚到历史版本（必须是已成功发布过的版本）。 */
  rollback(version: string): boolean {
    if (!this.versions.has(version)) return false;
    this.active = version;
    return true;
  }
}

/* ----------------------------- 增量重算 ----------------------------- */

export interface IncrementalResult {
  output: JsonValue;
  records: Record<string, FieldRecord>;
  errors: FieldError[];
  inversePatch: InverseOp[];
  fromVersion: string;
  toVersion: string;
  /** 因规则变更而依赖受影响、被重新计算的字段路径。 */
  recomputedPaths: string[];
  /** 依赖未变、直接复用上一版本结果的字段路径。 */
  unchangedPaths: string[];
}

function matchingSignature(rs: RuleSet, path: string): string {
  const segs = path.split("/").slice(1).map((s) => s.replace(/~1/g, "/").replace(/~0/g, "~"));
  const parts: string[] = [];
  rs.rules.forEach((rule, i) => {
    const p = rule.path.split("/").slice(1);
    if (p.length !== segs.length) return;
    const hit = p.every((tok, k) => tok === "*" || tok === segs[k]);
    if (hit) parts.push(`${i}:${JSON.stringify(rule)}`);
  });
  return parts.join("||");
}

/**
 * 规范化会话：记住原始树与已应用版本。
 * 局部规则更新时只重算“命中规则签名发生变化”的字段，
 * 其余字段复用上一版本结果；非法规则无法发布，因此更新目标总是已验证版本。
 */
export class NormalizationSession {
  private currentVersion: PublishedVersion;
  private last: NormalizeResult;

  private registry: RuleRegistry;
  private original: JsonValue;

  constructor(registry: RuleRegistry, original: JsonValue, initialVersion?: string) {
    this.registry = registry;
    this.original = original;
    const v = initialVersion
      ? registry.getVersion(initialVersion)
      : registry.getActive();
    if (!v) throw new Error("注册表中没有可用于初始化的已发布版本");
    this.currentVersion = v;
    this.last = normalize(this.original, v.ruleset, { rulesetVersion: v.version });
  }

  get version(): string {
    return this.currentVersion.version;
  }

  get result(): NormalizeResult {
    return this.last;
  }

  updateTo(version: string): IncrementalResult {
    const target = this.registry.getVersion(version);
    if (!target) throw new Error(`版本 ${version} 未成功发布，拒绝更新`);
    const previousVersion = this.currentVersion.version;

    const fromRs = this.currentVersion.ruleset;
    const toRs = target.ruleset;
    const paths: string[] = [];
    walkLeaves(this.original, (path) => paths.push(path));

    const affected = paths.filter((p) => matchingSignature(fromRs, p) !== matchingSignature(toRs, p));
    const unaffected = paths.filter((p) => !affected.includes(p));

    // 对受影响字段从“原始值”重新跑新版本规则：先在原树副本上完整规范化，
    // 再只取受影响字段覆盖到当前输出，保证局部重算语义清晰。
    const fresh = normalize(this.original, toRs, { rulesetVersion: target.version });

    const records: Record<string, FieldRecord> = {};
    for (const p of unaffected) records[p] = this.last.records[p];
    for (const p of affected) records[p] = fresh.records[p];

    const errors: FieldError[] = [];
    const inversePatch: InverseOp[] = [];
    const output: JsonValue = structuredClone(this.last.output);
    for (const p of affected) {
      const rec = fresh.records[p];
      // 用新版本最终值覆盖
      const cur = rec.after.value;
      const origVal = rec.before.value;
      if (JSON.stringify(cur) !== JSON.stringify(origVal)) {
        setLeaf(output, p, cur);
        inversePatch.push({ op: "replace", path: p, value: origVal });
      } else if (JSON.stringify(cur) !== JSON.stringify(getLeaf(this.last.output, p))) {
        setLeaf(output, p, origVal);
      }
      if (rec.error) errors.push(rec.error);
    }
    // 未受影响字段保持旧输出，其逆操作仍来自上一版本记录。
    for (const p of unaffected) {
      const rec = this.last.records[p];
      if (JSON.stringify(rec.after.value) !== JSON.stringify(rec.before.value)) {
        inversePatch.push({ op: "replace", path: p, value: rec.before.value });
      }
      if (rec.error) errors.push(rec.error);
    }

    this.currentVersion = target;
    this.last = {
      output,
      records,
      errors,
      inversePatch,
      rulesetVersion: target.version,
    };

    return {
      output,
      records,
      errors,
      inversePatch,
      fromVersion: previousVersion,
      toVersion: target.version,
      recomputedPaths: affected,
      unchangedPaths: unaffected,
    };
  }
}

function getLeaf(root: JsonValue, path: string): JsonValue {
  const segs = path.split("/").slice(1).map((s) => s.replace(/~1/g, "/").replace(/~0/g, "~"));
  let node: any = root;
  for (const s of segs) node = Array.isArray(node) ? node[Number(s)] : node[s];
  return node as JsonValue;
}

function setLeaf(root: JsonValue, path: string, value: JsonValue): void {
  const segs = path.split("/").slice(1).map((s) => s.replace(/~1/g, "/").replace(/~0/g, "~"));
  let node: any = root;
  for (let i = 0; i < segs.length - 1; i++) node = Array.isArray(node) ? node[Number(segs[i])] : node[segs[i]];
  const last = segs[segs.length - 1];
  if (Array.isArray(node)) node[Number(last)] = value;
  else node[last] = value;
}

export { identityOf };
