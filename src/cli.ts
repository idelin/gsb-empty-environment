#!/usr/bin/env node
// CLI:
//   node src/cli.ts check   <old.json> <new.json>
//   node src/cli.ts convert <old.json> <new.json> <message.json>
//   node src/cli.ts verify  <old.json> <new.json> [--max-len N] [--max-messages N]
// Exit codes: 0 = compatible / converted / verified match
//             1 = incompatible / conversion failed / verify mismatch
//             2 = unsupported schema, usage or IO error

import { readFileSync } from 'node:fs';
import { checkCompat } from './check.ts';
import { parseSchema } from './schema.ts';
import { convertMessage } from './convert.ts';
import { verifyExhaustive, DEFAULT_CAPS } from './enumerate.ts';

function usage(): never {
  console.error(`usage:
  schema-tool check   <old.json> <new.json>
  schema-tool convert <old.json> <new.json> <message.json>
  schema-tool verify  <old.json> <new.json> [--max-len N] [--max-messages N]`);
  process.exit(2);
}

function readJson(path: string): unknown {
  try {
    return JSON.parse(readFileSync(path, 'utf8'));
  } catch (e) {
    console.error(`error: cannot read/parse ${path}: ${(e as Error).message}`);
    process.exit(2);
  }
}

const print = (x: unknown) => console.log(JSON.stringify(x, null, 2));

const [cmd, ...args] = process.argv.slice(2);

switch (cmd) {
  case 'check': {
    if (args.length !== 2) usage();
    const r = checkCompat(readJson(args[0]), readJson(args[1]));
    print(r);
    process.exit(r.result === 'compatible' ? 0 : r.result === 'incompatible' ? 1 : 2);
  }
  case 'convert': {
    if (args.length !== 3) usage();
    const o = parseSchema(readJson(args[0]));
    if (!o.ok) {
      print({ result: 'unsupported', reason: `old schema: ${o.reason}` });
      process.exit(2);
    }
    const n = parseSchema(readJson(args[1]));
    if (!n.ok) {
      print({ result: 'unsupported', reason: `new schema: ${n.reason}` });
      process.exit(2);
    }
    const r = convertMessage(o.schema, n.schema, readJson(args[2]));
    print(r);
    process.exit(r.ok ? 0 : 1);
  }
  case 'verify': {
    if (args.length < 2) usage();
    const caps = { ...DEFAULT_CAPS };
    for (let i = 2; i < args.length; i++) {
      const v = Number(args[++i]);
      if (!Number.isSafeInteger(v) || v < 0) usage();
      if (args[i - 1] === '--max-len') caps.maxLen = v;
      else if (args[i - 1] === '--max-messages') caps.maxMessages = v;
      else usage();
    }
    const r = verifyExhaustive(readJson(args[0]), readJson(args[1]), caps);
    print(r);
    process.exit(r.result === 'match' ? 0 : r.result === 'mismatch' ? 1 : 2);
  }
  default:
    usage();
}
