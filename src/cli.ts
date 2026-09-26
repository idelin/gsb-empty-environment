#!/usr/bin/env node
import { readFileSync } from "node:fs";
import { compareSchemas, renderReport, type Sample } from "./diff.ts";
import type { SchemaNode } from "./validator.ts";

function usage(): never {
  console.error("usage: node --experimental-strip-types src/cli.ts <old.schema.json> <new.schema.json> <samples.json> [--cache <dir>] [--no-cache]");
  process.exit(2);
}

function main(argv: string[]): void {
  const positional: string[] = [];
  let cacheDir: string | undefined = ".schemacache";
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--no-cache") cacheDir = undefined;
    else if (arg === "--cache") cacheDir = argv[++i];
    else positional.push(arg);
  }
  if (positional.length !== 3) usage();

  const oldSchema = JSON.parse(readFileSync(positional[0], "utf8")) as SchemaNode;
  const newSchema = JSON.parse(readFileSync(positional[1], "utf8")) as SchemaNode;
  const samples = JSON.parse(readFileSync(positional[2], "utf8")) as Sample[];

  const result = compareSchemas(oldSchema, newSchema, samples, { cacheDir });
  console.log(renderReport(result));

  if (result.report.newFailures.length > 0) process.exitCode = 1;
}

main(process.argv.slice(2));
