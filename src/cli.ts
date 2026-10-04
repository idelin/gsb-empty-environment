#!/usr/bin/env node
// CLI for the limited schema compatibility checker and converter.
//
// Usage:
//   schema-tool check    <old.json> <new.json>           compatibility verdict
//   schema-tool convert  <old.json> <new.json> <msg.json>  atomic conversion
//   schema-tool verify   <old.json> <new.json> [--budget N]  finite-domain check
//   schema-tool validate <schema.json>                   schema lint
//
// Exit codes: 0 = compatible / success, 1 = incompatible / conversion
// failed, 2 = unsupported or invalid schema, 3 = usage or I/O error.

import { readFileSync } from 'node:fs';
import { check } from './check.ts';
import { convertMessage } from './convert.ts';
import { parseSchema } from './validate.ts';
import { verify } from './verify.ts';

function readJson(path: string): unknown {
  try {
    return JSON.parse(readFileSync(path, 'utf8'));
  } catch (e) {
    console.error(`error: cannot read ${path}: ${(e as Error).message}`);
    process.exit(3);
  }
}

function print(x: unknown): void {
  console.log(JSON.stringify(x, null, 2));
}

const [cmd, ...args] = process.argv.slice(2);

switch (cmd) {
  case 'check': {
    if (args.length !== 2) {
      console.error('usage: schema-tool check <old.json> <new.json>');
      process.exit(3);
    }
    const result = check(readJson(args[0]), readJson(args[1]));
    print(result);
    process.exit(result.result === 'compatible' ? 0 : result.result === 'incompatible' ? 1 : 2);
    break;
  }

  case 'convert': {
    if (args.length !== 3) {
      console.error('usage: schema-tool convert <old.json> <new.json> <message.json>');
      process.exit(3);
    }
    const fromParsed = parseSchema(readJson(args[0]));
    const toParsed = parseSchema(readJson(args[1]));
    if (!fromParsed.schema || !toParsed.schema) {
      print({
        result: 'unsupported',
        old: fromParsed.issues,
        new: toParsed.issues,
      });
      process.exit(2);
    }
    const result = convertMessage(readJson(args[2]), fromParsed.schema, toParsed.schema);
    print(result);
    process.exit(result.ok ? 0 : 1);
    break;
  }

  case 'verify': {
    const files = args.filter((a) => !a.startsWith('--'));
    if (files.length !== 2) {
      console.error('usage: schema-tool verify <old.json> <new.json> [--budget N]');
      process.exit(3);
    }
    const budgetIdx = args.indexOf('--budget');
    const budget = budgetIdx >= 0 ? Number(args[budgetIdx + 1]) : undefined;
    const result = verify(readJson(files[0]), readJson(files[1]), {
      ...(budget !== undefined ? { valueCap: budget } : {}),
    });
    print(result);
    process.exit(result.consistent ? 0 : result.checkResult === 'unsupported' ? 2 : 1);
    break;
  }

  case 'validate': {
    if (args.length !== 1) {
      console.error('usage: schema-tool validate <schema.json>');
      process.exit(3);
    }
    const parsed = parseSchema(readJson(args[0]));
    print({ valid: parsed.schema !== null, issues: parsed.issues });
    process.exit(parsed.schema !== null ? 0 : 2);
    break;
  }

  default:
    console.error('usage: schema-tool <check|convert|verify|validate> ...');
    process.exit(3);
}
