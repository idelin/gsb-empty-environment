/**
 * CLI for the sharded set id store.
 *
 *   node dist/cli.js build    --set NAME --version V --out FILE (--ids "1,2,3" | --ids-file F) [--shard-size N]
 *   node dist/cli.js query    --expr "or(A,and(B,C))" --set A=fileA --set B=fileB --k N
 *                             [--cursor C] [--quota BYTES] [--cache-bytes N] [--tenant T] [--all]
 *   node dist/cli.js validate --file F
 *   node dist/cli.js validate --expr E --set A=fileA ... [--k N] [--quota BYTES]
 *
 * Query prints one JSON object per page with the ids, the next cursor and
 * stats including peak decoded bytes.
 */
import * as fs from 'fs';
import { buildSetFile } from './format';
import { parseExpr, exprSets } from './expr';
import { QueryEngine } from './engine';
import { SetStore } from './store';
import { BlockCache } from './cache';
import { loadSetIds, referenceEval, validateFile, validatePages } from './validate';

interface Args {
  _: string[];
  [key: string]: string | boolean | string[];
}

function parseArgs(argv: string[]): Args {
  const out: Args = { _: [] };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a.startsWith('--')) {
      const key = a.slice(2);
      const next = argv[i + 1];
      if (next === undefined || next.startsWith('--')) {
        out[key] = true;
      } else {
        if (out[key] === undefined) out[key] = next;
        else if (Array.isArray(out[key])) (out[key] as string[]).push(next);
        else out[key] = [out[key] as string, next];
        i++;
      }
    } else {
      out._.push(a);
    }
  }
  return out;
}

function need(args: Args, key: string): string {
  const v = args[key];
  if (typeof v !== 'string') throw new Error('missing required --' + key);
  return v;
}

function optInt(args: Args, key: string, dflt: number): number {
  const v = args[key];
  if (v === undefined) return dflt;
  if (typeof v !== 'string' || !/^-?\d+$/.test(v)) throw new Error('--' + key + ' must be an integer');
  return parseInt(v, 10);
}

function parseSetInputs(args: Args): Record<string, string> {
  const raw = args.set;
  const list = raw === undefined ? [] : Array.isArray(raw) ? raw : [raw as string];
  const out: Record<string, string> = {};
  for (const item of list) {
    const eq = item.indexOf('=');
    if (eq <= 0) throw new Error('--set must be NAME=FILE, got "' + item + '"');
    out[item.slice(0, eq)] = item.slice(eq + 1);
  }
  return out;
}

function cmdBuild(args: Args): void {
  const setId = need(args, 'set');
  const version = need(args, 'version');
  const outFile = need(args, 'out');
  const shardSize = optInt(args, 'shard-size', 1000);
  let ids: number[] = [];
  if (typeof args.ids === 'string') {
    ids = (args.ids as string).split(',').filter((s) => s.trim() !== '').map((s) => parseInt(s.trim(), 10));
  } else if (typeof args['ids-file'] === 'string') {
    const text = fs.readFileSync(args['ids-file'] as string, 'utf8');
    ids = text.split(/[\s,]+/).filter((s) => s !== '').map((s) => parseInt(s, 10));
  } else {
    throw new Error('provide --ids "1,2,3" or --ids-file F');
  }
  ids = [...ids].sort((a, b) => a - b);
  const buf = buildSetFile(setId, version, ids, shardSize);
  fs.writeFileSync(outFile, buf);
  console.log(JSON.stringify({ ok: true, file: outFile, setId, version, ids: ids.length, bytes: buf.length }));
}

function cmdQuery(args: Args): void {
  const expr = parseExpr(need(args, 'expr'));
  const inputs = parseSetInputs(args);
  const quota = optInt(args, 'quota', 1 << 20);
  const cacheBytes = optInt(args, 'cache-bytes', 1 << 20);
  const tenant = typeof args.tenant === 'string' ? (args.tenant as string) : 'default';
  const k = args.k === undefined ? undefined : optInt(args, 'k', 0);
  const all = args.all === true;

  const cache = new BlockCache(cacheBytes);
  const engine = new QueryEngine(new SetStore(cache));

  let cursor: string | undefined = typeof args.cursor === 'string' ? (args.cursor as string) : undefined;
  let pageIndex = 0;
  for (;;) {
    const page = engine.queryPage({ expr, inputs, k, cursor, tenant, quotaBytes: quota });
    console.log(JSON.stringify({
      page: pageIndex,
      ids: page.ids,
      count: page.ids.length,
      nextCursor: page.nextCursor,
      stats: page.stats,
    }));
    pageIndex++;
    if (!all || page.nextCursor === null) break;
    cursor = page.nextCursor;
  }
}

function cmdValidate(args: Args): void {
  if (typeof args.file === 'string') {
    const result = validateFile(args.file as string);
    console.log(JSON.stringify(result, null, 2));
    if (!result.ok) process.exitCode = 1;
    return;
  }
  const expr = parseExpr(need(args, 'expr'));
  const inputs = parseSetInputs(args);
  const quota = optInt(args, 'quota', 1 << 20);
  const k = optInt(args, 'k', 100);

  const errors: string[] = [];
  const setArrays = new Map<string, number[]>();
  for (const setId of exprSets(expr)) {
    const path = inputs[setId];
    if (!path) {
      errors.push('no input file provided for set "' + setId + '"');
      continue;
    }
    const fv = validateFile(path);
    if (!fv.ok) errors.push(...fv.errors.map((e) => path + ': ' + e));
    setArrays.set(setId, loadSetIds(path));
  }
  if (errors.length > 0) {
    console.log(JSON.stringify({ ok: false, errors }, null, 2));
    process.exitCode = 1;
    return;
  }
  const reference = referenceEval(expr, setArrays);

  const cache = new BlockCache(1 << 20);
  const engine = new QueryEngine(new SetStore(cache));
  const pages: number[][] = [];
  let cursor: string | undefined;
  let peak = 0;
  for (;;) {
    const page = engine.queryPage({ expr, inputs, k, cursor, quotaBytes: quota });
    pages.push(page.ids);
    peak = Math.max(peak, page.stats.peakDecodedBytes);
    if (page.nextCursor === null) break;
    cursor = page.nextCursor;
  }
  const pageErrors = validatePages(pages, reference);
  const ok = pageErrors.length === 0;
  console.log(JSON.stringify({
    ok,
    errors: pageErrors,
    referenceCount: reference.length,
    pages: pages.length,
    peakDecodedBytes: peak,
  }, null, 2));
  if (!ok) process.exitCode = 1;
}

function main(): void {
  const args = parseArgs(process.argv.slice(2));
  const cmd = args._[0];
  try {
    if (cmd === 'build') cmdBuild(args);
    else if (cmd === 'query') cmdQuery(args);
    else if (cmd === 'validate') cmdValidate(args);
    else {
      console.error('usage: cli.js <build|query|validate> ...');
      process.exitCode = 2;
    }
  } catch (e) {
    const err = e as Error;
    console.error(JSON.stringify({ ok: false, error: err.name + ': ' + err.message }));
    process.exitCode = 1;
  }
}

main();
