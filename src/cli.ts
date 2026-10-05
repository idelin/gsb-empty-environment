#!/usr/bin/env node
/**
 * Usage:
 *   node src/cli.ts <input> [-o <output>] [--map <map.json>]
 *                   [--var NAME=VALUE]... [--vars <vars.json>]
 *
 * VALUE is parsed as `true`/`false`, a number, or otherwise kept as a string.
 */

import { readFileSync, writeFileSync } from 'node:fs';
import { pruneSource } from './prune.ts';
import { PruneError } from './errors.ts';
import type { VarTable, VarValue } from './expr.ts';

function parseValue(raw: string): VarValue {
  if (raw === 'true') return true;
  if (raw === 'false') return false;
  const num = Number(raw);
  if (raw !== '' && !Number.isNaN(num)) return num;
  return raw;
}

function main(argv: string[]): number {
  const vars: VarTable = {};
  let input: string | undefined;
  let output: string | undefined;
  let mapFile: string | undefined;

  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '-o') {
      output = argv[++i];
    } else if (arg === '--map') {
      mapFile = argv[++i];
    } else if (arg === '--var') {
      const pair = argv[++i];
      const eq = pair === undefined ? -1 : pair.indexOf('=');
      if (eq <= 0) {
        console.error(`invalid --var '${pair}', expected NAME=VALUE`);
        return 2;
      }
      vars[pair.slice(0, eq)] = parseValue(pair.slice(eq + 1));
    } else if (arg === '--vars') {
      const file = argv[++i];
      Object.assign(vars, JSON.parse(readFileSync(file, 'utf8')) as VarTable);
    } else if (arg.startsWith('-')) {
      console.error(`unknown option '${arg}'`);
      return 2;
    } else {
      input = arg;
    }
  }

  if (input === undefined) {
    console.error('usage: node src/cli.ts <input> [-o output] [--map map.json] [--var NAME=VALUE]... [--vars vars.json]');
    return 2;
  }

  try {
    const result = pruneSource(readFileSync(input, 'utf8'), { vars });
    if (output === undefined) process.stdout.write(result.text);
    else writeFileSync(output, result.text);
    if (mapFile !== undefined) {
      writeFileSync(mapFile, JSON.stringify(result.mapping.toJSON(), null, 2) + '\n');
    }
    return 0;
  } catch (err) {
    if (err instanceof PruneError) {
      console.error(`${input}: ${err.message}`);
      return 1;
    }
    throw err;
  }
}

process.exit(main(process.argv.slice(2)));
