/**
 * Domain model for the cache-key auditor.
 *
 * A build is modelled as a pure function from a finite `Environment`
 * (an assignment of one value to every declared input) to a `BuildOutput`
 * consisting of an `artifact` (the thing that gets cached) and a `log`
 * (diagnostic output). Inputs typically include environment variables,
 * tool versions and files that may be read conditionally.
 */

export type InputValue = string | number | boolean;

/** Maps every input name to its finite, enumerable set of possible values. */
export type InputDomain = Readonly<Record<string, readonly InputValue[]>>;

/** A concrete assignment of one value per input. */
export type Environment = Readonly<Record<string, InputValue>>;

export interface BuildOutput {
  /** Canonical serialization of the produced artifact (what gets cached). */
  readonly artifact: string;
  /** Canonical serialization of the build log (diagnostics only). */
  readonly log: string;
}

export interface BuildSpec {
  readonly name: string;
  readonly domain: InputDomain;
  /** Names of inputs that are hashed into the cache key. */
  readonly keyInputs: readonly string[];
  /**
   * The build itself. It must access inputs exclusively through property
   * reads on `env` (e.g. `env["env:CI"]`, `"env:CI" in env`) so the auditor
   * can trace which inputs are actually used, including conditional reads.
   */
  readonly build: (env: Environment) => BuildOutput;
}

/** How a key-colliding pair of environments differs in output. */
export type DifferenceClass = "artifact-affecting" | "log-only";

export function classifyDifference(a: BuildOutput, b: BuildOutput): DifferenceClass | null {
  if (a.artifact !== b.artifact) return "artifact-affecting";
  if (a.log !== b.log) return "log-only";
  return null;
}
