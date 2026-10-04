#!/usr/bin/env bun
import { readFile, writeFile } from "node:fs/promises";
import { bundleFromJson, bundleToJson, ProofBundle } from "./bundle";
import { fromHex, toHex } from "./hash";
import { commitSource, proveRange } from "./prover";
import { FileChunkSource, MemoryChunkSource } from "./source";
import { verifyRange } from "./verifier";

const DEFAULT_CHUNK_SIZE = 64 * 1024;

function usage(): never {
  console.error(`chunked file verifier

usage:
  cli.ts commit  <file> [--chunk-size N]
  cli.ts prove   <file> --range A:B [--chunk-size N] > bundle.json
  cli.ts verify  <commitment.json> <bundle.json> --range A:B

commit prints the trusted commitment JSON (length + chunk size + tree root).
prove emits a proof bundle carrying only the requested chunks and their proofs.
verify checks the bundle against the trusted commitment and reports per-chunk
verdicts: ok / bad / missing / insufficient.`);
  process.exit(2);
}

function parseRange(args: string[]): [number, number] {
  const i = args.indexOf("--range");
  if (i < 0 || !args[i + 1]) usage();
  const m = /^(\d+):(\d+)$/.exec(args[i + 1]);
  if (!m) usage();
  return [Number(m[1]), Number(m[2])];
}

function parseChunkSize(args: string[]): number {
  const i = args.indexOf("--chunk-size");
  return i >= 0 && args[i + 1] ? Number(args[i + 1]) : DEFAULT_CHUNK_SIZE;
}

async function main(): Promise<void> {
  const [command, ...rest] = process.argv.slice(2);
  if (!command) usage();

  if (command === "commit") {
    const file = rest[0];
    if (!file) usage();
    const chunkSize = parseChunkSize(rest);
    const source = await FileChunkSource.open(file, chunkSize);
    const proven = await commitSource(source, source.length, chunkSize);
    console.log(JSON.stringify({
      fileLength: proven.header.fileLength,
      chunkSize: proven.header.chunkSize,
      treeRoot: toHex(proven.header.treeRoot),
      commitment: toHex(proven.commitment),
    }, null, 2));
    return;
  }

  if (command === "prove") {
    const file = rest[0];
    if (!file) usage();
    const chunkSize = parseChunkSize(rest);
    const [start, end] = parseRange(rest);
    const source = await FileChunkSource.open(file, chunkSize);
    const proven = await commitSource(source, source.length, chunkSize);
    const chunks = new Map<number, Uint8Array>();
    for (let i = start; i < Math.min(end, proven.leafCount); i++) {
      const data = await source.getChunk(i);
      if (data !== undefined) chunks.set(i, data);
    }
    const proofs = new Map(proveRange(proven, start, end).map((p) => [p.index, p]));
    const bundle: ProofBundle = {
      header: proven.header,
      commitment: proven.commitment,
      chunks,
      proofs,
    };
    console.log(bundleToJson(bundle));
    return;
  }

  if (command === "verify") {
    const [commitmentPath, bundlePath] = rest;
    if (!commitmentPath || !bundlePath) usage();
    const [start, end] = parseRange(rest);
    const committed = JSON.parse(await readFile(commitmentPath, "utf8"));
    const bundle = bundleFromJson(await readFile(bundlePath, "utf8"));
    const report = await verifyRange({
      commitment: fromHex(committed.commitment),
      header: bundle.header,
      start,
      end,
      chunks: new MemoryChunkSource(bundle.chunks),
      proofs: bundle.proofs,
    });
    console.log(JSON.stringify({
      commitmentValid: report.commitmentValid,
      range: [report.start, report.end],
      ok: report.ok,
      bad: report.bad,
      missing: report.missing,
      insufficient: report.insufficient,
    }, null, 2));
    if (!report.commitmentValid || report.bad.length > 0) process.exit(1);
    if (report.missing.length > 0 || report.insufficient.length > 0) process.exit(3);
    return;
  }

  usage();
}

main().catch((err) => {
  console.error(err.message ?? err);
  process.exit(1);
});
