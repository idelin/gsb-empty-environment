#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { trimSource, TrimError } from './index.ts';

function usage(): never {
  console.error('usage: node src/cli.ts <file> [-D NAME[=true|false]]... [--map]');
  process.exit(2);
}

const vars: Record<string, boolean> = {};

function applyDefine(spec: string): void {
  const eq = spec.indexOf('=');
  if (eq === -1) {
    vars[spec] = true;
    return;
  }
  const name = spec.slice(0, eq);
  const value = spec.slice(eq + 1);
  if (value !== 'true' && value !== 'false') {
    console.error(`invalid value for ${name}: '${value}' (expected true|false)`);
    process.exit(2);
  }
  vars[name] = value === 'true';
}

const args = process.argv.slice(2);
let file: string | undefined;
let showMap = false;
for (let i = 0; i < args.length; i++) {
  const arg = args[i];
  if (arg === '--map') {
    showMap = true;
  } else if (arg === '-D' || arg === '--define') {
    const spec = args[++i];
    if (!spec) usage();
    applyDefine(spec);
  } else if (arg.startsWith('-D')) {
    applyDefine(arg.slice(2));
  } else if (arg.startsWith('-')) {
    usage();
  } else if (file === undefined) {
    file = arg;
  } else {
    usage();
  }
}
if (file === undefined) usage();

const source = readFileSync(file, 'utf8');
try {
  const result = trimSource(source, vars);
  process.stdout.write(result.text);
  if (showMap) {
    console.error(JSON.stringify({ oldToNew: result.oldToNew, newToOld: result.newToOld }));
  }
} catch (err) {
  if (err instanceof TrimError) {
    console.error(`error [${err.code}]: ${err.message}`);
    process.exit(1);
  }
  throw err;
}
