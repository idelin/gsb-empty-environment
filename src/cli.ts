#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { diffTrajectories } from './index.js';
import type { CompareOptions, TraceInput } from './types.js';

interface CliInput {
  options?: CompareOptions;
  left?: TraceInput;
  right?: TraceInput;
}

function usage(): string {
  return [
    'Usage: trace-diff [input.json]',
    '',
    'Reads a JSON document from the given file, or from stdin when no file is given:',
    '  {',
    '    "options": { "observable": ["navigate", "click"], "ignoreKeys": [],',
    '                 "numericTolerance": 0, "maxWindow": 20, "lookahead": 10 },',
    '    "left":  <trace>,',
    '    "right": <trace>',
    '  }',
    '',
    'A <trace> is one of:',
    '  [event, ...]                         plain event list',
    '  [segment, ...]                       segment list (run / loop)',
    '  { "events": [event, ...] }',
    '  { "segments": [segment, ...] }',
    '',
    'Segments: { "kind": "run", "events": [...] } | { "kind": "loop", "body": [...], "times": N }',
    'Events:   { "action": "click", "args": {...}, "result": ..., "status": "ok"|"error", "error": "..." }',
    '',
    'Exit code: 0 = semantically equal, 1 = divergence found, 2 = usage/input error.',
  ].join('\n');
}

function main(argv: string[]): number {
  const args = argv.slice(2);
  if (args.indexOf('--help') !== -1 || args.indexOf('-h') !== -1) {
    process.stdout.write(usage() + '\n');
    return 0;
  }
  if (args.length > 1) {
    process.stderr.write(usage() + '\n');
    return 2;
  }
  let raw: string;
  try {
    raw = args.length === 0 ? readFileSync(0, 'utf8') : readFileSync(args[0], 'utf8');
  } catch (err) {
    process.stderr.write('trace-diff: cannot read input: ' + String(err) + '\n');
    return 2;
  }
  let input: CliInput;
  try {
    input = JSON.parse(raw);
  } catch (err) {
    process.stderr.write('trace-diff: invalid JSON: ' + String(err) + '\n');
    return 2;
  }
  if (input === null || typeof input !== 'object' || input.left === undefined || input.right === undefined) {
    process.stderr.write('trace-diff: input must contain "left" and "right" traces\n');
    return 2;
  }
  try {
    const report = diffTrajectories(input.left, input.right, input.options);
    process.stdout.write(JSON.stringify(report, null, 2) + '\n');
    return report.equal ? 0 : 1;
  } catch (err) {
    process.stderr.write('trace-diff: ' + (err instanceof Error ? err.message : String(err)) + '\n');
    return 2;
  }
}

process.exit(main(process.argv));
