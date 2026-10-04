#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { solveAllocation } from './allocator.js';
import { greedyAllocate } from './greedy.js';
import { inputError, isInputError } from './validate.js';
import type { AllocationInput, BudgetOptions } from './types.js';

function usage(): string {
  return [
    'Usage:',
    '  node dist/cli.js [input.json|-] [--budget N] [--time-ms MS] [--baseline] [--compact]',
    '',
    'Reads the allocation problem as JSON from input.json, or stdin when omitted or "-".',
    '',
    'Input JSON:',
    '  requests: [{ "id": "r1", "region": "cn-north", "demand": 3 }]',
    '  nodes:    [{ "id": "n1", "region": "cn-north", "capacity": 5 }]',
    '  costs:    { "cn-north": { "cn-south": 2 }, "cn-south": { "cn-north": 2 } }',
    '  previous: { "r1": "n1" }            (optional prior plan; null = was rejected)',
    '  budget:   { "maxStates": 100000, "timeMs": 1000 }  (optional)',
    '',
    'Objectives are lexicographic: maximize admitted demand, then minimize',
    'cross-region cost, then minimize migrations vs the previous plan, then the',
    'node-id vector ordered by request id. Output is the SolveResult JSON.',
  ].join('\n');
}

function fail(message: string): never {
  console.error(`error: ${message}`);
  process.exit(2);
}

export function main(argv: string[]): number {
  let inputPath: string | null = null;
  let budgetFlag: number | undefined;
  let timeFlag: number | undefined;
  let baseline = false;
  let compact = false;

  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '--budget') {
      budgetFlag = Number(argv[++i]);
    } else if (arg === '--time-ms') {
      timeFlag = Number(argv[++i]);
    } else if (arg === '--baseline') {
      baseline = true;
    } else if (arg === '--compact') {
      compact = true;
    } else if (arg === '--help' || arg === '-h') {
      console.log(usage());
      return 0;
    } else if (arg.startsWith('--')) {
      return fail(`unknown flag: ${arg}`);
    } else if (inputPath === null) {
      inputPath = arg;
    } else {
      return fail(`unexpected argument: ${arg}`);
    }
  }

  let raw: string;
  try {
    raw = inputPath === null || inputPath === '-'
      ? readFileSync(0, 'utf8')
      : readFileSync(inputPath, 'utf8');
  } catch (err) {
    return fail(`cannot read input: ${String(err)}`);
  }

  let parsed: AllocationInput & { budget?: BudgetOptions };
  try {
    parsed = JSON.parse(raw) as AllocationInput & { budget?: BudgetOptions };
  } catch (err) {
    return fail(`input is not valid JSON: ${String(err)}`);
  }

  const budget: BudgetOptions = {};
  if (parsed.budget !== undefined) {
    if (parsed.budget.maxStates !== undefined) budget.maxStates = parsed.budget.maxStates;
    if (parsed.budget.timeMs !== undefined) budget.timeMs = parsed.budget.timeMs;
  }
  if (budgetFlag !== undefined) {
    if (!Number.isInteger(budgetFlag) || budgetFlag < 1) {
      return fail('--budget must be a positive integer');
    }
    budget.maxStates = budgetFlag;
  }
  if (timeFlag !== undefined) {
    if (!(timeFlag > 0)) return fail('--time-ms must be a positive number');
    budget.timeMs = timeFlag;
  }

  try {
    const result = solveAllocation(parsed, budget);
    const out: Record<string, unknown> = { result };
    if (baseline) out.baseline = greedyAllocate(parsed);
    console.log(JSON.stringify(out, null, compact ? 0 : 2));
    return 0;
  } catch (err) {
    if (isInputError(err)) {
      const message = err instanceof Error ? err.message : String(err);
      return fail(message);
    }
    throw err;
  }
}

process.exitCode = main(process.argv.slice(2));
