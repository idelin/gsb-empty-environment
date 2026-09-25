import { pathToFileURL } from "node:url";
import {
  compileFull,
  ConfigStore,
  jsonEquals,
  StrategyMap,
  type CommitOp,
  type JsonValue,
  type Snapshot,
  type StrategyRule,
  type Validator,
} from "../src/index.ts";

/** A replayable scenario: operations as plain data, executed by `replay`.
 *  Every mutating step is verified against a from-scratch compile, so the
 *  sample doubles as an executable specification. */
export interface ReplayStep {
  name: string;
  commit?: CommitOp[];
  rollback?: boolean;
  /** expected ConfigError code when the batch must be rejected */
  expectError?: string;
  /** expected config after the step */
  expectConfig?: JsonValue;
  /** expected incremental recompute set */
  expectRecomputed?: string[];
}

export const RULES: StrategyRule[] = [
  { pattern: "/services", strategy: "byId" },
  { pattern: "/features/*", strategy: "override" },
];

const noCursedPorts: Validator = (config) => {
  const port = (config as Record<string, any>).server?.port;
  if (port === 1) throw new Error("port 1 is cursed");
};

export const script: ReplayStep[] = [
  {
    name: "seed base layer",
    commit: [{ kind: "upsertLayer", layer: { id: "base", priority: 0, data: {
      server: { host: "localhost", port: 8080, opts: { tls: false, retry: 1 } },
      services: [{ id: "web", type: "http", weight: 1 }, { id: "db", type: "tcp" }],
      features: { ui: { a: 1 } },
      meta: { primary: { $ref: "#/server/host" } },
      note: null,
    } } }],
    expectConfig: {
      server: { host: "localhost", port: 8080, opts: { tls: false, retry: 1 } },
      services: [{ id: "web", type: "http", weight: 1 }, { id: "db", type: "tcp" }],
      features: { ui: { a: 1 } },
      meta: { primary: "localhost" },
      note: null,
    },
  },
  {
    name: "add env layer: deep merge + byId union",
    commit: [{ kind: "upsertLayer", layer: { id: "env", priority: 10, data: {
      server: { port: 9090, opts: { tls: true } },
      services: [{ id: "web", weight: 5 }, { id: "cache", type: "tcp" }],
      features: { ui: { b: 2 } },
    } } }],
    expectConfig: {
      server: { host: "localhost", port: 9090, opts: { tls: true, retry: 1 } },
      services: [
        { id: "web", type: "http", weight: 5 },
        { id: "db", type: "tcp" },
        { id: "cache", type: "tcp" },
      ],
      features: { ui: { b: 2 } },
      meta: { primary: "localhost" },
      note: null,
    },
  },
  {
    name: "add conditional beta layer (off: flags.beta missing)",
    commit: [{ kind: "upsertLayer", layer: { id: "beta", priority: 20, when: { path: "flags.beta", equals: true }, data: {
      features: { ui: { betaPanel: true } },
      server: { port: 7000 },
    } } }],
    expectConfig: {
      server: { host: "localhost", port: 9090, opts: { tls: true, retry: 1 } },
      services: [
        { id: "web", type: "http", weight: 5 },
        { id: "db", type: "tcp" },
        { id: "cache", type: "tcp" },
      ],
      features: { ui: { b: 2 } },
      meta: { primary: "localhost" },
      note: null,
    },
  },
  {
    name: "enable beta via context",
    commit: [{ kind: "setContext", context: { flags: { beta: true } } }],
    expectConfig: {
      server: { host: "localhost", port: 7000, opts: { tls: true, retry: 1 } },
      services: [
        { id: "web", type: "http", weight: 5 },
        { id: "db", type: "tcp" },
        { id: "cache", type: "tcp" },
      ],
      features: { ui: { betaPanel: true } },
      meta: { primary: "localhost" },
      note: null,
    },
  },
  {
    name: "hot update env layer: only /server/port is recomputed",
    commit: [{ kind: "setLayerData", id: "env", data: {
      server: { port: 9091, opts: { tls: true } },
      services: [{ id: "web", weight: 5 }, { id: "cache", type: "tcp" }],
      features: { ui: { b: 2 } },
    } }],
    expectRecomputed: ["/server/port"],
  },
  {
    name: "hot update base host: dependent ref re-resolves",
    commit: [{ kind: "setLayerData", id: "base", data: {
      server: { host: "example.org", port: 8080, opts: { tls: false, retry: 1 } },
      services: [{ id: "web", type: "http", weight: 1 }, { id: "db", type: "tcp" }],
      features: { ui: { a: 1 } },
      meta: { primary: { $ref: "#/server/host" } },
      note: null,
    } }],
    expectRecomputed: ["/server/host"],
  },
  {
    name: "delete sentinel removes a field and a byId item; null stays",
    commit: [{ kind: "setLayerData", id: "env", data: {
      server: { port: 9091, opts: { $delete: true } },
      services: [{ id: "web", weight: 5 }, { id: "cache", type: "tcp" }, { id: "db", $delete: true }],
      features: { ui: { b: 2 } },
    } }],
    expectConfig: {
      server: { host: "example.org", port: 7000 },
      services: [{ id: "web", type: "http", weight: 5 }, { id: "cache", type: "tcp" }],
      features: { ui: { betaPanel: true } },
      meta: { primary: "example.org" },
      note: null,
    },
  },
  {
    name: "byId same-id type conflict rejects the whole batch",
    commit: [
      { kind: "upsertLayer", layer: { id: "bad", priority: 30, data: { services: [{ id: "web", type: "grpc" }] } } },
      { kind: "setLayerData", id: "base", data: {
        server: { host: "evil", port: 8080, opts: { tls: false, retry: 1 } },
        services: [{ id: "web", type: "http", weight: 1 }, { id: "db", type: "tcp" }],
        features: { ui: { a: 1 } },
        meta: { primary: { $ref: "#/server/host" } },
        note: null,
      } },
    ],
    expectError: "TYPE_CONFLICT",
  },
  {
    name: "validator failure rejects the commit; old config keeps serving",
    commit: [{ kind: "setLayerData", id: "beta", data: {
      features: { ui: { betaPanel: true } },
      server: { port: 1 },
    } }],
    expectError: "VALIDATION",
  },
  {
    name: "swap priorities: base outranks env",
    commit: [{ kind: "swapPriority", a: "base", b: "env" }],
    expectConfig: {
      server: { host: "example.org", port: 7000, opts: { tls: false, retry: 1 } },
      services: [
        { id: "web", type: "http", weight: 1 },
        { id: "cache", type: "tcp" },
        { id: "db", type: "tcp" },
      ],
      features: { ui: { betaPanel: true } },
      meta: { primary: "example.org" },
      note: null,
    },
  },
  {
    name: "disable beta via context: its fields disappear",
    commit: [{ kind: "setContext", context: { flags: { beta: false } } }],
    expectConfig: {
      server: { host: "example.org", port: 8080, opts: { tls: false, retry: 1 } },
      services: [
        { id: "web", type: "http", weight: 1 },
        { id: "cache", type: "tcp" },
        { id: "db", type: "tcp" },
      ],
      features: { ui: { a: 1 } },
      meta: { primary: "example.org" },
      note: null,
    },
  },
  { name: "rollback: beta is back" , rollback: true },
  { name: "rollback: priorities restored", rollback: true },
];

export interface ReplayReport {
  lines: string[];
  snapshots: Snapshot[];
}

export function replay(
  steps: ReplayStep[],
  opts: { strategies?: StrategyRule[]; validators?: Validator[] } = {},
): ReplayReport {
  const rules = opts.strategies ?? RULES;
  const store = new ConfigStore({ strategies: rules, validators: opts.validators ?? [noCursedPorts] });
  const lines: string[] = [];
  const snapshots: Snapshot[] = [];
  const push = (s: string) => lines.push(s);

  const checkScratch = (label: string): void => {
    const scratch = compileFull(store.getLayerStates(), store.getContext(), new StrategyMap(rules));
    const snap = store.getSnapshot();
    if (!jsonEquals(snap.config, scratch.config)) {
      throw new Error(`${label}: incremental result diverges from from-scratch compile`);
    }
  };

  for (const step of steps) {
    if (step.rollback) {
      const snap = store.rollback();
      snapshots.push(snap);
      checkScratch(step.name);
      push(`[v${snap.version}] ${step.name} -> rolled back, config=${JSON.stringify(snap.config)}`);
      continue;
    }
    const before = store.getSnapshot();
    try {
      const snap = store.commit(step.commit ?? []);
      if (step.expectError) throw new Error(`${step.name}: expected error ${step.expectError} but commit succeeded`);
      snapshots.push(snap);
      checkScratch(step.name);
      if (step.expectConfig !== undefined && !jsonEquals(snap.config, step.expectConfig)) {
        throw new Error(`${step.name}: config mismatch\n  want ${JSON.stringify(step.expectConfig)}\n  got  ${JSON.stringify(snap.config)}`);
      }
      if (step.expectRecomputed !== undefined) {
        const got = [...snap.stats.recomputedPaths].sort();
        const want = [...step.expectRecomputed].sort();
        if (JSON.stringify(got) !== JSON.stringify(want)) {
          throw new Error(`${step.name}: recomputed ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
        }
      }
      push(`[v${snap.version}] ${step.name} -> ok, config=${JSON.stringify(snap.config)}`);
    } catch (err) {
      const code = (err as { code?: string }).code;
      if (step.expectError && (code === step.expectError || (step.expectError === "VALIDATION" && code === undefined))) {
        if (store.getSnapshot().version !== before.version) {
          throw new Error(`${step.name}: rejected commit must not publish a new version`);
        }
        push(`[v${before.version}] ${step.name} -> rejected (${step.expectError}): ${(err as Error).message}`);
        continue;
      }
      throw err;
    }
  }
  return { lines, snapshots };
}

const isMain = process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href;
if (isMain) {
  const report = replay(script);
  for (const line of report.lines) console.log(line);
  console.log(`\nREPLAY OK: ${report.lines.length} steps, every step verified against a from-scratch compile`);
}
