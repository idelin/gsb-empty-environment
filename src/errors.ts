import { pathToPointer, pointerToPath } from "./json.ts";
import type { Path } from "./json.ts";

/**
 * Every failure carries the exact field path (and, when relevant, the layer)
 * so errors can be located precisely.
 */
export class ConfigError extends Error {
  readonly path: Path;
  readonly pointer: string;
  readonly layerId: string | undefined;

  constructor(message: string, path: Path, layerId?: string) {
    const pointer = pathToPointer(path) || "/";
    const where = layerId === undefined ? pointer : `${pointer} in layer "${layerId}"`;
    super(`${message} (at ${where})`);
    this.name = new.target.name;
    this.path = path;
    this.pointer = pathToPointer(path);
    this.layerId = layerId;
  }
}

/** Two array items share an id but have conflicting value types. */
export class MergeTypeConflictError extends ConfigError {}

/** A field value violates its merge strategy's shape requirements. */
export class FieldValidationError extends ConfigError {}

/** A `$ref` is malformed or points at a path that does not exist. */
export class RefResolutionError extends ConfigError {}

/** Reference cycle; `cycle` is the full chain of field pointers involved. */
export class RefCycleError extends ConfigError {
  readonly cycle: readonly string[];

  constructor(cycle: readonly string[]) {
    super(`reference cycle detected: ${cycle.join(" -> ")}`, pointerToPath(cycle[0] ?? ""));
    this.cycle = cycle;
  }
}
