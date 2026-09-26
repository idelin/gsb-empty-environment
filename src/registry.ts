import { schemaDigest, type Schema } from "./schema.ts";
import { validate, type ValidationError } from "./validator.ts";

export interface MigrationError {
  /** 形如 "v1->v2" 的迁移步骤标识 */
  step: string;
  message: string;
  /** 若失败原因是迁移产物未通过目标 schema，这里携带完整验证错误 */
  validationErrors?: ValidationError[];
}

export type LoadResult =
  | {
      ok: true;
      document: unknown;
      sourceVersion: string;
      targetVersion: string;
      appliedMigrations: string[];
    }
  | { ok: false; stage: "version"; errors: string[] }
  | { ok: false; stage: "migration"; errors: MigrationError[]; originalDocument: unknown }
  | { ok: false; stage: "validation"; errors: ValidationError[] };

export interface LoadOptions {
  /** 版本字段名，默认 "version" */
  versionField?: string;
  /** 目标版本，默认注册表中最新的版本 */
  targetVersion?: string;
  /**
   * 文档缺版本字段时的显式回退版本。
   * 缺省不设置：缺版本一律拒绝，绝不静默当作最新版。
   */
  defaultVersion?: string;
}

type MigrationFn = (doc: unknown) => unknown;

interface RegisteredSchema {
  schema: Schema;
  digest: string;
}

/** 同版本号注册到不同内容（摘要不一致）时抛出。 */
export class SchemaConflictError extends Error {
  readonly version: string;
  readonly existingDigest: string;
  readonly incomingDigest: string;

  constructor(version: string, existingDigest: string, incomingDigest: string) {
    super(
      `schema conflict for version "${version}": ` +
        `existing digest ${existingDigest.slice(0, 12)}… != incoming ${incomingDigest.slice(0, 12)}…`,
    );
    this.name = "SchemaConflictError";
    this.version = version;
    this.existingDigest = existingDigest;
    this.incomingDigest = incomingDigest;
  }
}

export class SchemaRegistry {
  private readonly schemas = new Map<string, RegisteredSchema>();
  private readonly migrations = new Map<string, { to: string; fn: MigrationFn }[]>();

  /**
   * 注册某版本的 schema。
   * 同版本同内容（摘要一致）→ 幂等通过；同版本不同内容 → 抛 SchemaConflictError。
   */
  registerSchema(version: string, schema: Schema): string {
    const digest = schemaDigest(schema);
    const existing = this.schemas.get(version);
    if (existing) {
      if (existing.digest !== digest) {
        throw new SchemaConflictError(version, existing.digest, digest);
      }
      return digest;
    }
    this.schemas.set(version, { schema, digest });
    return digest;
  }

  /** 注册一步显式迁移；两端版本必须已注册 schema。 */
  registerMigration(fromVersion: string, toVersion: string, fn: MigrationFn): void {
    for (const v of [fromVersion, toVersion]) {
      if (!this.schemas.has(v)) {
        throw new Error(`cannot register migration: version "${v}" has no registered schema`);
      }
    }
    const edges = this.migrations.get(fromVersion) ?? [];
    edges.push({ to: toVersion, fn });
    this.migrations.set(fromVersion, edges);
  }

  get supportedVersions(): string[] {
    return [...this.schemas.keys()];
  }

  /** 最新版本 = 最后注册的版本（显式注册顺序，而非隐式猜测）。 */
  get latestVersion(): string | undefined {
    return this.supportedVersions.at(-1);
  }

  digestOf(version: string): string | undefined {
    return this.schemas.get(version)?.digest;
  }

  /**
   * 载入文档：版本选择 → 显式迁移链 → 目标 schema 再验证。
   * 三个阶段独立报告错误；迁移失败时原始文档原样保留在结果中。
   */
  load(input: unknown, options: LoadOptions = {}): LoadResult {
    const versionField = options.versionField ?? "version";

    // ── 阶段 1：版本选择 ─────────────────────────────
    if (typeof input !== "object" || input === null || Array.isArray(input)) {
      return { ok: false, stage: "version", errors: ["document must be a plain object"] };
    }
    const rawVersion = (input as Record<string, unknown>)[versionField];
    let sourceVersion: string;
    if (rawVersion === undefined || rawVersion === null) {
      if (options.defaultVersion === undefined) {
        return {
          ok: false,
          stage: "version",
          errors: [
            `missing version field "${versionField}"; ` +
              `refusing to assume latest — pass defaultVersion explicitly to opt in`,
          ],
        };
      }
      sourceVersion = options.defaultVersion;
    } else if (typeof rawVersion !== "string") {
      return {
        ok: false,
        stage: "version",
        errors: [`version field "${versionField}" must be a string, got ${typeof rawVersion}`],
      };
    } else {
      sourceVersion = rawVersion;
    }

    if (!this.schemas.has(sourceVersion)) {
      return {
        ok: false,
        stage: "version",
        errors: [
          `unknown version "${sourceVersion}"; supported: ${this.supportedVersions.join(", ")}`,
        ],
      };
    }

    const targetVersion = options.targetVersion ?? this.latestVersion;
    if (targetVersion === undefined || !this.schemas.has(targetVersion)) {
      return {
        ok: false,
        stage: "version",
        errors: [`unknown target version "${String(targetVersion)}"`],
      };
    }

    // ── 阶段 2：显式迁移链（在深拷贝上执行，原文不动） ──
    const chain = this.findChain(sourceVersion, targetVersion);
    if (chain === null) {
      return {
        ok: false,
        stage: "migration",
        errors: [
          {
            step: `${sourceVersion}->${targetVersion}`,
            message: `no explicit migration chain from "${sourceVersion}" to "${targetVersion}"`,
          },
        ],
        originalDocument: input,
      };
    }

    let current: unknown = structuredClone(input);
    const applied: string[] = [];
    let currentVersion = sourceVersion;
    for (const edge of chain) {
      const stepId = `${currentVersion}->${edge.to}`;
      try {
        current = edge.fn(current);
      } catch (cause) {
        return {
          ok: false,
          stage: "migration",
          errors: [
            {
              step: stepId,
              message: `migration threw: ${cause instanceof Error ? cause.message : String(cause)}`,
            },
          ],
          originalDocument: input,
        };
      }
      // 每步迁移后立即用该步目标版本的 schema 再验证
      const stepErrors = validate(current, this.schemas.get(edge.to)!.schema);
      if (stepErrors.length > 0) {
        return {
          ok: false,
          stage: "migration",
          errors: [
            {
              step: stepId,
              message: `migration output failed schema of "${edge.to}"`,
              validationErrors: stepErrors,
            },
          ],
          originalDocument: input,
        };
      }
      applied.push(stepId);
      currentVersion = edge.to;
    }

    // ── 阶段 3：目标 schema 最终验证 ─────────────────
    const errors = validate(current, this.schemas.get(targetVersion)!.schema);
    if (errors.length > 0) {
      return { ok: false, stage: "validation", errors };
    }

    return {
      ok: true,
      document: current,
      sourceVersion,
      targetVersion,
      appliedMigrations: applied,
    };
  }

  /** BFS 找显式迁移链；找不到返回 null（绝不隐式补链）。 */
  private findChain(
    from: string,
    to: string,
  ): { to: string; fn: MigrationFn }[] | null {
    if (from === to) return [];
    const queue: { version: string; path: { to: string; fn: MigrationFn }[] }[] = [
      { version: from, path: [] },
    ];
    const visited = new Set([from]);
    while (queue.length > 0) {
      const { version, path } = queue.shift()!;
      for (const edge of this.migrations.get(version) ?? []) {
        if (visited.has(edge.to)) continue;
        const nextPath = [...path, edge];
        if (edge.to === to) return nextPath;
        visited.add(edge.to);
        queue.push({ version: edge.to, path: nextPath });
      }
    }
    return null;
  }
}
