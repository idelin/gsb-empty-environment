import { readFileSync } from 'node:fs';
import { solveGrant } from './solver.ts';
import type { GrantRequest } from './types.ts';

const input = process.argv[2]
  ? readFileSync(process.argv[2], 'utf8')
  : readFileSync(0, 'utf8');

const request = JSON.parse(input) as GrantRequest;
const result = solveGrant(request);
process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);

process.exitCode = result.ok ? 0 : 1;
