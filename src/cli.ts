import { readFileSync } from 'node:fs';
import type { Snapshot } from './types.ts';
import { buildPlan } from './plan.ts';
import { applyPlan } from './apply.ts';

function readJson(path: string): unknown {
  try {
    return JSON.parse(readFileSync(path, 'utf8'));
  } catch (err) {
    throw new Error(`cannot read ${path}: ${(err as Error).message}`);
  }
}

function readSnapshot(path: string): Snapshot {
  const data = readJson(path) as Snapshot;
  if (!data || !Array.isArray(data.nodes)) {
    throw new Error(`invalid snapshot file: ${path} (expected { "nodes": [...] })`);
  }
  return data;
}

function usage(): void {
  process.stderr.write(
    [
      'usage:',
      '  snapdiff diff  <old.json> <new.json>                 print changes + ordered plan (JSON)',
      '  snapdiff apply <old.json> <plan.json> <new.json>   apply plan, verify against target tree',
      '',
    ].join('\n'),
  );
}

const [command, ...args] = process.argv.slice(2);

try {
  if (command === 'diff' && args.length === 2) {
    const plan = buildPlan(readSnapshot(args[0]), readSnapshot(args[1]));
    process.stdout.write(`${JSON.stringify(plan, null, 2)}\n`);
  } else if (command === 'apply' && args.length === 3) {
    const oldSnap = readSnapshot(args[0]);
    const plan = readJson(args[1]) as Parameters<typeof applyPlan>[1];
    const expected = readSnapshot(args[2]);
    const result = applyPlan(oldSnap, plan, expected);
    process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
  } else {
    usage();
    process.exitCode = 2;
  }
} catch (err) {
  const error = err as Error;
  process.stderr.write(`${error.name}: ${error.message}\n`);
  process.exitCode = 1;
}
