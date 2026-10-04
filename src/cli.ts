#!/usr/bin/env node
/**
 * CLI: reads a JSON job from stdin and writes the rebase result to stdout.
 *
 * Input:
 *   {
 *     "base": "shared base text",              // optional, used for validation
 *     "strategy": "left-priority" | "conflict", // optional, default "conflict"
 *     "left":  { "id": "...", "edits": [{ "id", "start", "end", "text" }] },
 *     "right": { "id": "...", "edits": [...] }
 *   }
 *
 * Output: { "patch": ..., "unpreserved": ..., "conflicts": ... }
 * Exit code is 2 when the 'conflict' strategy reported conflicts, else 0.
 */
import process from 'node:process';
import type { Patch } from './model.ts';
import { validatePatch } from './model.ts';
import type { Strategy } from './rebase.ts';
import { rebase } from './rebase.ts';

interface Job {
  base?: string;
  strategy?: Strategy;
  left: Patch;
  right: Patch;
}

function readStdin(): Promise<string> {
  return new Promise((resolve, reject) => {
    let data = '';
    process.stdin.setEncoding('utf8');
    process.stdin.on('data', (chunk) => (data += chunk));
    process.stdin.on('end', () => resolve(data));
    process.stdin.on('error', reject);
  });
}

const job = JSON.parse(await readStdin()) as Job;
const strategy: Strategy = job.strategy ?? 'conflict';
if (strategy !== 'conflict' && strategy !== 'left-priority') {
  console.error(`unknown strategy: ${JSON.stringify(job.strategy)}`);
  process.exit(64);
}
if (typeof job.base === 'string') {
  validatePatch(job.base, job.left);
  validatePatch(job.base, job.right);
}
const result = rebase(job.left, job.right, strategy);
console.log(JSON.stringify(result, null, 2));
if (result.conflicts.length > 0) process.exit(2);
