import type { Registry } from "./registry.ts";
import { validate, type ValidationError } from "./validator.ts";

/**
 * Processing phases. Errors are reported per phase; a failure in an earlier
 * phase stops the pipeline before later phases run.
 */
export type Phase =
  | "version-resolution"
  | "validation"
  | "migration"
  | "revalidation";

export interface ProcessError extends ValidationError {
  phase: Phase;
}

export interface ProcessOptions {
  /** Document field carrying the version. Defaults to "version". */
  versionField?: string;
  /**
   * Explicit fallback version when the document has none. There is NO
   * implicit "latest" default: a missing version is rejected unless the
   * caller opts in here by naming the version to assume.
   */
  assumeVersion?: string;
  /**
   * Target version to migrate to. Defaults to the last registered version.
   * Must be reachable through registered migration steps.
   */
  targetVersion?: string;
}

export interface ProcessSuccess {
  ok: true;
  document: Record<string, unknown>;
  fromVersion: string;
  toVersion: string;
  migrated: boolean;
  migrationPath: string[];
}

export interface ProcessFailure {
  ok: false;
  phase: Phase;
  errors: ProcessError[];
  /** The original document, untouched, when migration/validation failed. */
  document: unknown;
}

export type ProcessResult = ProcessSuccess | ProcessFailure;

function failure(phase: Phase, errors: ProcessError[], document: unknown): ProcessFailure {
  return { ok: false, phase, errors, document };
}

function phaseError(
  phase: Phase,
  keyword: string,
  message: string,
  instancePath = "",
  schemaPath = "#",
): ProcessError {
  return { phase, keyword, message, instancePath, schemaPath };
}

function deepClone<T>(value: T): T {
  return value === undefined ? value : JSON.parse(JSON.stringify(value));
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Full pipeline:
 *   1. version-resolution — read the version field, reject missing/unknown.
 *   2. validation         — validate against the schema of that version.
 *   3. migration          — walk the explicit chain to the target version.
 *   4. revalidation       — validate the migrated document against the
 *                           target schema.
 * On any migration/revalidation failure the ORIGINAL document is preserved
 * in the result.
 */
export function processDocument(
  registry: Registry,
  input: unknown,
  options: ProcessOptions = {},
): ProcessResult {
  const versionField = options.versionField ?? "version";

  // ---- Phase 1: version resolution ----
  if (!isPlainObject(input)) {
    return failure("version-resolution", [
      phaseError("version-resolution", "type", "document must be a plain object"),
    ], input);
  }
  const original = deepClone(input);

  let version: string;
  let document = input;
  const rawVersion = input[versionField];
  if (rawVersion === undefined || rawVersion === null) {
    if (options.assumeVersion === undefined) {
      return failure("version-resolution", [
        phaseError(
          "version-resolution",
          "required",
          `missing version field "${versionField}"; ` +
            `no implicit default exists — pass assumeVersion explicitly to opt in`,
          `/${versionField}`,
        ),
      ], original);
    }
    version = options.assumeVersion;
    // Treat the document as if it declared the assumed version.
    document = { ...input, [versionField]: version };
  } else if (typeof rawVersion !== "string") {
    return failure("version-resolution", [
      phaseError(
        "version-resolution",
        "type",
        `version field "${versionField}" must be a string`,
        `/${versionField}`,
      ),
    ], original);
  } else {
    version = rawVersion;
  }

  if (!registry.hasVersion(version)) {
    return failure("version-resolution", [
      phaseError(
        "version-resolution",
        "enum",
        `unknown version "${version}"; known versions: ${registry.versions().join(", ")}`,
        `/${versionField}`,
      ),
    ], original);
  }

  // ---- Phase 2: validation against the document's own version ----
  const sourceSchema = registry.getSchema(version)!;
  const validationErrors = validate(sourceSchema, document).map((e) => ({
    ...e,
    phase: "validation" as const,
  }));
  if (validationErrors.length > 0) {
    return failure("validation", validationErrors, original);
  }

  // ---- Phase 3: explicit migration chain ----
  const target = options.targetVersion ?? registry.versions().at(-1)!;
  if (!registry.hasVersion(target)) {
    return failure("migration", [
      phaseError("migration", "enum", `unknown target version "${target}"`),
    ], original);
  }
  const path = registry.findMigrationPath(version, target);
  if (path === null) {
    return failure("migration", [
      phaseError(
        "migration",
        "chain",
        `no explicit migration chain from "${version}" to "${target}"`,
      ),
    ], original);
  }

  // Migrate on a copy; the original stays intact if anything fails.
  let working = deepClone(document);
  const visited: string[] = [version];
  for (const step of path) {
    let migrated: Record<string, unknown>;
    try {
      migrated = step.migrate(deepClone(working));
    } catch (err) {
      return failure("migration", [
        phaseError(
          "migration",
          "step",
          `migration ${step.from} -> ${step.to} threw: ${(err as Error).message}`,
        ),
      ], original);
    }
    if (!isPlainObject(migrated)) {
      return failure("migration", [
        phaseError(
          "migration",
          "step",
          `migration ${step.from} -> ${step.to} did not return an object`,
        ),
      ], original);
    }
    working = migrated;
    visited.push(step.to);
  }

  // ---- Phase 4: revalidation against the target schema ----
  const targetSchema = registry.getSchema(target)!;
  const revalidationErrors = validate(targetSchema, working).map((e) => ({
    ...e,
    phase: "revalidation" as const,
  }));
  if (revalidationErrors.length > 0) {
    return failure("revalidation", revalidationErrors, original);
  }

  return {
    ok: true,
    document: working,
    fromVersion: version,
    toVersion: target,
    migrated: path.length > 0,
    migrationPath: visited,
  };
}
