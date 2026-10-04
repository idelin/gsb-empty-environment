#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { formatDecisionTree, solveDecisionTree } from './solver.js';

const args = process.argv.slice(2);
const asJson = args.includes('--json');
const file = args.find((a) => !a.startsWith('--'));

if (args.includes('--help') || args.includes('-h')) {
  console.log('Usage: cli.js [problem.json] [--json]   (reads stdin when no file is given)');
  process.exit(0);
}

const input = file ? readFileSync(file, 'utf8') : readFileSync(0, 'utf8');
const problem = JSON.parse(input);
const result = solveDecisionTree(problem);
console.log(asJson ? JSON.stringify(result, null, 2) : formatDecisionTree(result));
