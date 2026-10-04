import { BuildSpec, Environment, InputValue } from "./model";

/** Result of executing a build once, with input reads traced. */
export interface TracedRun {
  readonly env: Environment;
  /** Canonical serialization of the cache key (projection on keyInputs). */
  readonly key: string;
  readonly artifact: string;
  readonly log: string;
  /** Inputs actually read during this run (conditional reads included). */
  readonly reads: ReadonlySet<string>;
}

/** Enumerates the full cartesian product of the domain, in stable order. */
export function enumerateEnvironments(
  domain: Readonly<Record<string, readonly InputValue[]>>,
  maxEnvironments: number,
): Environment[] {
  const names = Object.keys(domain).sort();
  let total = 1;
  for (const name of names) {
    const values = domain[name]!;
    if (values.length === 0) {
      throw new Error(`Input "${name}" has an empty domain; domains must be non-empty.`);
    }
    total *= values.length;
    if (total > maxEnvironments) {
      throw new Error(
        `Input domain too large to enumerate (${total}+ environments, limit ${maxEnvironments}). ` +
          `Reduce the domains or raise the limit.`,
      );
    }
  }
  const envs: Environment[] = [];
  const current: Record<string, InputValue> = {};
  const walk = (index: number): void => {
    if (index === names.length) {
      envs.push({ ...current });
      return;
    }
    const name = names[index]!;
    for (const value of domain[name]!) {
      current[name] = value;
      walk(index + 1);
    }
  };
  walk(0);
  return envs;
}

/** Projects an environment onto the declared key inputs, canonically serialized. */
export function computeKey(env: Environment, keyInputs: readonly string[]): string {
  const parts: string[] = [];
  for (const name of [...keyInputs].sort()) {
    parts.push(`${name}=${JSON.stringify(env[name] ?? null)}`);
  }
  return parts.join("\n");
}

/** Runs the build once, tracing every input read via a Proxy. */
export function traceRun(spec: BuildSpec, env: Environment): TracedRun {
  const reads = new Set<string>();
  const traced = new Proxy(env, {
    get(target, prop, receiver) {
      if (typeof prop === "string") reads.add(prop);
      return Reflect.get(target, prop, receiver);
    },
    has(target, prop) {
      if (typeof prop === "string") reads.add(prop);
      return Reflect.has(target, prop);
    },
  });
  const output = spec.build(traced);
  return {
    env,
    key: computeKey(env, spec.keyInputs),
    artifact: output.artifact,
    log: output.log,
    reads,
  };
}
