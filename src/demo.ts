import { DecodeError } from "./errors.ts";
import { decodeMessage, encodeMessage, validateMessage } from "./schema.ts";
import { recordTrace, serializeTrace, deserializeTrace, verifyReplay } from "./sim/replay.ts";
import { buildQueueSimulation } from "./sim/workload.ts";

const line = (s: string) => console.log(s);

line("=== 1. Deterministic simulation (seed = 42) ===");
const record = recordTrace(buildQueueSimulation(42, true), 100_000, "queue-demo");
line(`steps=${record.meta.steps} finalHash=0x${record.meta.finalHash.toString(16).padStart(8, "0")}`);
line(`first entry: ${JSON.stringify(record.entries[0])}`);
line(`last  entry: ${JSON.stringify(record.entries[record.entries.length - 1])}`);

line("\n=== 2. Persist trace as hand-rolled protobuf wire bytes ===");
const bytes = serializeTrace(record);
line(`encoded ${record.entries.length} trace entries -> ${bytes.length} bytes`);
const restored = deserializeTrace(bytes);
line(`decoded entries=${restored.entries.length} checksums(packed)=${restored.checksums.length}`);
line(`meta: seed=${restored.meta.seed} steps=${restored.meta.steps} magic=0x${restored.meta.magic.toString(16)}`);
line(`roundtrip identical: ${JSON.stringify(restored.entries) === JSON.stringify(record.entries)}`);

line("\n=== 3. Bug reproduction: replay and compare per-step fingerprints ===");
const replay = verifyReplay(() => buildQueueSimulation(42, true), restored);
line(`replay ok=${replay.ok} stepsCompared=${replay.stepsCompared}`);

line("\n=== 4. Fault injection: tamper one trace entry, replay pinpoints it ===");
const tampered = deserializeTrace(bytes);
tampered.entries[500] = { ...tampered.entries[500], hash: tampered.entries[500].hash ^ 0xff };
const failed = verifyReplay(() => buildQueueSimulation(42, true), tampered);
line(`replay ok=${failed.ok} divergence=${JSON.stringify(failed.divergence)}`);

line("\n=== 5. Safe out-of-bounds protection on truncated fields ===");
for (const cut of [1, 3, 7, bytes.length - 1]) {
  try {
    deserializeTrace(bytes.subarray(0, cut));
    line(`cut=${cut}: decoded (prefix happened to be well-formed)`);
  } catch (err) {
    const e = err as DecodeError;
    line(`cut=${cut}: ${e.name}: ${e.message}`);
  }
}

line("\n=== 6. Reflection validation engine ===");
const bad = {
  meta: { seed: -5n, steps: 10n, finalHash: 1, name: 99, drift: 0n, version: 1, magic: 1n },
  entries: [{ step: 1n, time: 1n, type: "arrival", hash: 2 ** 40 }],
  checksums: [1, 2, 3],
};
const violations = validateMessage(
  (await import("./sim/replay.ts")).TRACE_SCHEMA,
  bad as never,
);
for (const v of violations) line(`violation: ${v}`);

line("\nDemo complete.");
