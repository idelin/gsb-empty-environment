import {
  BuildOutput,
  BuildSpec,
  DifferenceClass,
  Environment,
  InputValue,
  classifyDifference,
} from "./model";
import { TracedRun, computeKey, enumerateEnvironments, traceRun } from "./run";

export interface Counterexample {
  readonly kind: DifferenceClass;
  /** The shared cache-key assignment held by both environments. */
  readonly key: Readonly<Record<string, InputValue>>;
  readonly a: Environment;
  readonly b: Environment;
  /** Inputs on which `a` and `b` disagree (all outside the key). */
  readonly diffInputs: readonly string[];
  readonly outputA: BuildOutput;
  readonly outputB: BuildOutput;
}

export type InputEffect = "artifact-affecting" | "log-only" | "none";

export interface AuditReport {
  readonly specName: string;
  readonly environmentCount: number;
  readonly keyCount: number;
  readonly keyInputs: readonly string[];
  /** Locally minimal counterexamples: key unchanged, output changed. */
  readonly counterexamples: readonly Counterexample[];
  /** Effect of each input on the output, observed across the whole domain. */
  readonly inputEffects: Readonly<Record<string, InputEffect>>;
  /** Inputs read by the build that change the output but are absent from the key. */
  readonly undeclaredEffectiveInputs: readonly string[];
  /** Inputs declared in the key that never influence the output. */
  readonly unusedKeyInputs: readonly string[];
  /** Inputs never read in any run (dead configuration surface). */
  readonly neverReadInputs: readonly string[];
}

export interface AuditOptions {
  /** Safety bound on the enumerated cartesian product. Default 200_000. */
  readonly maxEnvironments?: number;
  /** Max candidate pairs minimized per key group. Default 8. */
  readonly maxCandidatesPerKey?: number;
}

interface MutablePair {
  a: Environment;
  b: Environment;
}

function envSignature(env: Environment, skip?: string): string {
  const parts: string[] = [];
  for (const name of Object.keys(env).sort()) {
    if (name === skip) continue;
    parts.push(`${name}=${JSON.stringify(env[name])}`);
  }
  return parts.join("\n");
}

function outputOf(run: TracedRun): BuildOutput {
  return { artifact: run.artifact, log: run.log };
}

/**
 * Greedily shrinks a key-colliding, output-differing pair by aligning
 * coordinates one at a time, until no single alignment preserves the
 * difference class. The result is 1-minimal: removing any remaining
 * difference destroys the counterexample.
 */
function minimize(
  spec: BuildSpec,
  pair: MutablePair,
  kind: DifferenceClass,
  outputCache: Map<string, BuildOutput>,
): MutablePair {
  const cachedOutput = (env: Environment): BuildOutput => {
    const sig = envSignature(env);
    let out = outputCache.get(sig);
    if (!out) {
      const run = traceRun(spec, env);
      out = outputOf(run);
      outputCache.set(sig, out);
    }
    return out;
  };

  let { a, b } = pair;
  let changed = true;
  while (changed) {
    changed = false;
    for (const name of Object.keys(a)) {
      if (a[name] === b[name]) continue;
      const outA = cachedOutput(a);
      const outB = cachedOutput(b);
      const bAligned: Environment = { ...b, [name]: a[name]! };
      if (classifyDifference(outA, cachedOutput(bAligned)) === kind) {
        b = bAligned;
        changed = true;
        break;
      }
      const aAligned: Environment = { ...a, [name]: b[name]! };
      if (classifyDifference(cachedOutput(aAligned), outB) === kind) {
        a = aAligned;
        changed = true;
        break;
      }
    }
  }
  return { a, b };
}

function counterexampleId(
  kind: DifferenceClass,
  key: string,
  a: Environment,
  b: Environment,
  diffInputs: readonly string[],
): string {
  const values = diffInputs
    .map((n) => `${n}:${JSON.stringify(a[n])}->${JSON.stringify(b[n])}`)
    .join(",");
  return `${kind}|${key}|${[...diffInputs].sort().join(",")}|${values}`;
}

export function audit(spec: BuildSpec, options: AuditOptions = {}): AuditReport {
  const maxEnvironments = options.maxEnvironments ?? 200_000;
  const maxCandidatesPerKey = options.maxCandidatesPerKey ?? 8;

  const envs = enumerateEnvironments(spec.domain, maxEnvironments);
  const runs = envs.map((env) => traceRun(spec, env));

  const outputCache = new Map<string, BuildOutput>();
  for (const run of runs) outputCache.set(envSignature(run.env), outputOf(run));

  // Group runs by cache key.
  const byKey = new Map<string, TracedRun[]>();
  for (const run of runs) {
    const group = byKey.get(run.key);
    if (group) group.push(run);
    else byKey.set(run.key, [run]);
  }

  // Within each key group, pair up runs whose outputs differ.
  const counterexamples = new Map<string, Counterexample>();
  for (const [key, group] of byKey) {
    const byArtifact = new Map<string, TracedRun[]>();
    for (const run of group) {
      const bucket = byArtifact.get(run.artifact);
      if (bucket) bucket.push(run);
      else byArtifact.set(run.artifact, [run]);
    }
    if (byArtifact.size < 2 && new Set(group.map((r) => r.log)).size < 2) continue;

    const candidates: Array<{ pair: MutablePair; kind: DifferenceClass }> = [];
    const artifacts = [...byArtifact.values()];
    for (let i = 0; i < artifacts.length && candidates.length < maxCandidatesPerKey; i++) {
      for (let j = i + 1; j < artifacts.length && candidates.length < maxCandidatesPerKey; j++) {
        candidates.push({
          pair: { a: artifacts[i]![0]!.env, b: artifacts[j]![0]!.env },
          kind: "artifact-affecting",
        });
      }
    }
    for (const bucket of artifacts) {
      if (candidates.length >= maxCandidatesPerKey) break;
      const byLog = new Map<string, TracedRun>();
      for (const run of bucket) if (!byLog.has(run.log)) byLog.set(run.log, run);
      const logs = [...byLog.values()];
      for (let i = 0; i + 1 < logs.length && candidates.length < maxCandidatesPerKey; i++) {
        candidates.push({
          pair: { a: logs[i]!.env, b: logs[i + 1]!.env },
          kind: "log-only",
        });
      }
    }

    for (const { pair, kind } of candidates) {
      const minimal = minimize(spec, pair, kind, outputCache);
      // Canonicalize direction so flipped duplicates collapse.
      if (envSignature(minimal.a) > envSignature(minimal.b)) {
        const tmp = minimal.a;
        minimal.a = minimal.b;
        minimal.b = tmp;
      }
      const outA = outputCache.get(envSignature(minimal.a))!;
      const outB = outputCache.get(envSignature(minimal.b))!;
      const diffInputs = Object.keys(minimal.a)
        .filter((n) => minimal.a[n] !== minimal.b[n])
        .sort();
      const id = counterexampleId(kind, key, minimal.a, minimal.b, diffInputs);
      if (counterexamples.has(id)) continue;
      const keyRecord: Record<string, InputValue> = {};
      for (const name of spec.keyInputs) {
        const value = minimal.a[name];
        keyRecord[name] = value === undefined ? "<undeclared>" : value;
      }
      counterexamples.set(id, {
        kind,
        key: keyRecord,
        a: minimal.a,
        b: minimal.b,
        diffInputs,
        outputA: outA,
        outputB: outB,
      });
    }
  }

  // Sensitivity analysis: for each input, group runs by the assignment of all
  // other inputs and observe whether varying this input changes the output.
  const inputEffects: Record<string, InputEffect> = {};
  for (const name of Object.keys(spec.domain)) {
    const groups = new Map<string, { artifacts: Set<string>; logs: Set<string> }>();
    for (const run of runs) {
      const sig = envSignature(run.env, name);
      let bucket = groups.get(sig);
      if (!bucket) {
        bucket = { artifacts: new Set(), logs: new Set() };
        groups.set(sig, bucket);
      }
      bucket.artifacts.add(run.artifact);
      bucket.logs.add(run.log);
    }
    let effect: InputEffect = "none";
    for (const bucket of groups.values()) {
      if (bucket.artifacts.size > 1) {
        effect = "artifact-affecting";
        break;
      }
      if (bucket.logs.size > 1) effect = "log-only";
    }
    inputEffects[name] = effect;
  }

  const readSomewhere = new Set<string>();
  for (const run of runs) for (const r of run.reads) readSomewhere.add(r);

  const keySet = new Set(spec.keyInputs);
  const undeclaredEffectiveInputs = Object.keys(inputEffects).filter(
    (n) => inputEffects[n] !== "none" && !keySet.has(n),
  );
  const unusedKeyInputs = spec.keyInputs.filter((n) => inputEffects[n] === "none");
  const neverReadInputs = Object.keys(spec.domain).filter((n) => !readSomewhere.has(n));

  const ordered = [...counterexamples.values()].sort((x, y) => {
    if (x.kind !== y.kind) return x.kind === "artifact-affecting" ? -1 : 1;
    return x.diffInputs.length - y.diffInputs.length;
  });

  return {
    specName: spec.name,
    environmentCount: runs.length,
    keyCount: byKey.size,
    keyInputs: spec.keyInputs,
    counterexamples: ordered,
    inputEffects,
    undeclaredEffectiveInputs,
    unusedKeyInputs,
    neverReadInputs,
  };
}
