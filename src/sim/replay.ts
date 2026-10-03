import type { MessageSchema } from "../schema.ts";
import { decodeMessage, encodeMessage } from "../schema.ts";
import type { Simulation, TraceEntry } from "./simulation.ts";

/**
 * Protobuf schema used to persist simulation traces — the same hand-rolled
 * codec powers the bug-reproduction pipeline end to end (nested messages,
 * packed repeated checksums, zigzag drift, fixed32/64 header fields).
 */
export const TRACE_SCHEMA: MessageSchema = {
  name: "TraceFile",
  fields: [
    {
      id: 1,
      name: "meta",
      type: "message",
      schema: {
        name: "Meta",
        fields: [
          { id: 1, name: "seed", type: "uint64" },
          { id: 2, name: "steps", type: "uint64" },
          { id: 3, name: "finalHash", type: "uint32" },
          { id: 4, name: "name", type: "string" },
          { id: 5, name: "drift", type: "sint64" },
          { id: 6, name: "version", type: "fixed32" },
          { id: 7, name: "magic", type: "fixed64" },
        ],
      },
    },
    {
      id: 2,
      name: "entries",
      type: "message",
      repeated: true,
      schema: {
        name: "TraceEntry",
        fields: [
          { id: 1, name: "step", type: "uint64" },
          { id: 2, name: "time", type: "uint64" },
          { id: 3, name: "type", type: "string" },
          { id: 4, name: "hash", type: "uint32" },
        ],
      },
    },
    { id: 3, name: "checksums", type: "uint32", repeated: true, packed: true },
  ],
};

export interface TraceRecord {
  meta: {
    seed: bigint;
    steps: bigint;
    finalHash: number;
    name: string;
    drift: bigint;
    version: number;
    magic: bigint;
  };
  entries: TraceEntry[];
  checksums: number[];
}

export const TRACE_MAGIC = 0xDE_C0_DE_5_1A_17n;
export const TRACE_VERSION = 1;

/** Run a simulation to completion (or maxSteps) and capture its trace. */
export function recordTrace(sim: Simulation, maxSteps: number, name = "trace"): TraceRecord {
  const stats = sim.run(maxSteps);
  const entries = sim.getTrace().map((e) => ({ ...e }));
  return {
    meta: {
      seed: BigInt(sim.seed),
      steps: BigInt(stats.steps),
      finalHash: stats.finalHash,
      name,
      drift: 0n,
      version: TRACE_VERSION,
      magic: TRACE_MAGIC,
    },
    entries,
    checksums: entries.map((e) => e.hash),
  };
}

/** Serialize a trace record to protobuf wire bytes. */
export function serializeTrace(record: TraceRecord): Uint8Array {
  return encodeMessage(TRACE_SCHEMA, record as never);
}

/** Restore a trace record from protobuf wire bytes. */
export function deserializeTrace(bytes: Uint8Array): TraceRecord {
  const obj = decodeMessage(TRACE_SCHEMA, bytes);
  const meta = obj.meta as Record<string, unknown>;
  return {
    meta: {
      seed: BigInt(meta.seed as bigint),
      steps: BigInt(meta.steps as bigint),
      finalHash: Number(meta.finalHash),
      name: String(meta.name),
      drift: BigInt(meta.drift as bigint),
      version: Number(meta.version),
      magic: BigInt(meta.magic as bigint),
    },
    entries: ((obj.entries as Record<string, unknown>[]) ?? []).map((e) => ({
      step: Number(e.step),
      time: Number(e.time),
      type: String(e.type),
      hash: Number(e.hash),
    })),
    checksums: ((obj.checksums as number[]) ?? []).map(Number),
  };
}

export interface Divergence {
  step: number;
  expectedHash: number;
  actualHash: number;
  expectedType: string;
  actualType: string | null;
}

export interface ReplayResult {
  ok: boolean;
  stepsCompared: number;
  divergence: Divergence | null;
}

/**
 * Bug reproduction: rebuild the simulation from `factory` (which must wire
 * the same handlers and seed), re-run it, and compare the per-step
 * fingerprints against a recorded trace. The first mismatch pinpoints the
 * exact step where behaviour diverged.
 */
export function verifyReplay(factory: () => Simulation, record: TraceRecord): ReplayResult {
  const sim = factory();
  const maxSteps = Number(record.meta.steps);
  let compared = 0;
  for (const expected of record.entries) {
    if (!sim.stepOnce()) {
      return {
        ok: false,
        stepsCompared: compared,
        divergence: {
          step: expected.step,
          expectedHash: expected.hash,
          actualHash: 0,
          expectedType: expected.type,
          actualType: null,
        },
      };
    }
    compared++;
    const actual = sim.getTrace()[sim.getTrace().length - 1];
    if (actual.hash !== expected.hash || actual.type !== expected.type) {
      return {
        ok: false,
        stepsCompared: compared,
        divergence: {
          step: expected.step,
          expectedHash: expected.hash,
          actualHash: actual.hash,
          expectedType: expected.type,
          actualType: actual.type,
        },
      };
    }
  }
  const ok = compared === maxSteps && sim.fingerprint() === record.meta.finalHash;
  return { ok, stepsCompared: compared, divergence: null };
}
