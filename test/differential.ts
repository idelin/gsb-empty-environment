import { TemplateEngine, EngineMode } from '../src/engine';
import { TemplateError } from '../src/errors';
import { Fragment } from '../src/runtime';
import { assertEqual, test } from './harness';

/**
 * Differential verification: the compiler's output must be observationally
 * identical to the direct AST interpreter (the reference model) — same text,
 * same provenance fragments, or the same error (class, message, span, stack).
 * The generator emphasises deep nesting, empty loops and variable shadowing.
 */

class Rng {
  private state: number;
  constructor(seed: number) {
    this.state = seed >>> 0 || 1;
  }
  next(): number {
    // xorshift32
    let x = this.state;
    x ^= x << 13;
    x ^= x >>> 17;
    x ^= x << 5;
    this.state = x >>> 0;
    return this.state / 0x100000000;
  }
  int(n: number): number {
    return Math.floor(this.next() * n);
  }
  pick<T>(items: T[]): T {
    return items[this.int(items.length)];
  }
  chance(p: number): boolean {
    return this.next() < p;
  }
}

const VAR_NAMES = ['a', 'b', 'item', 'user', 'x', 'list', 'missing'];
const LIST_NAMES = ['list', 'items', 'rows', 'missingList'];
const PARTIALS = ['p1', 'p2'];

interface GenEnv {
  rng: Rng;
  /** Loop variable names currently in scope (for shadowing-heavy templates). */
  loopVars: string[];
  depth: number;
  allowInclude: boolean;
}

function genPath(env: GenEnv, heads: string[]): string {
  let path = env.rng.pick(heads);
  if (path.startsWith('@')) return path;
  if (env.rng.chance(0.3)) path += '.' + env.rng.pick(['name', 'value', 'deep', 'cells']);
  return path;
}

function genNodes(env: GenEnv, count: number): string {
  let out = '';
  for (let i = 0; i < count; i++) {
    out += genNode(env);
  }
  return out;
}

function genNode(env: GenEnv): string {
  const { rng } = env;
  const atRoot = env.depth <= 0;
  const kind = atRoot
    ? rng.pick(['text', 'var', 'var'])
    : rng.pick(['text', 'var', 'var', 'if', 'each', 'include']);
  switch (kind) {
    case 'text':
      return rng.pick(['', 'x', ' ', '\\n', '<b>', 'hello ']);
    case 'var': {
      const heads = VAR_NAMES.concat(env.loopVars, env.loopVars.length > 0 ? ['@index'] : []);
      return `{{ ${genPath(env, heads)} }}`;
    }
    case 'if': {
      const cond = genPath(env, VAR_NAMES.concat(env.loopVars));
      const then = genNodes({ ...env, depth: env.depth - 1 }, rng.int(3));
      const els = rng.chance(0.7) ? genNodes({ ...env, depth: env.depth - 1 }, rng.int(3)) : '';
      const elsePart = rng.chance(0.7) ? `{{else}}${els}` : '';
      return `{{#if ${cond}}}${then}${elsePart}{{/if}}`;
    }
    case 'each': {
      const list = genPath(env, LIST_NAMES.concat(env.loopVars));
      // Reuse names from VAR_NAMES on purpose to force shadowing.
      const itemName = rng.pick(['item', 'x', 'a', 'v']);
      const body = genNodes(
        { ...env, depth: env.depth - 1, loopVars: env.loopVars.concat([itemName]) },
        rng.int(3) + 1,
      );
      return `{{#each ${list} as ${itemName}}}${body}{{/each}}`;
    }
    case 'include':
      if (!env.allowInclude) return 't';
      return `{{> ${rng.pick(PARTIALS)} }}`;
    default:
      return '';
  }
}

function genTemplate(rng: Rng, allowInclude: boolean): string {
  return genNodes({ rng, loopVars: [], depth: 3, allowInclude }, rng.int(5) + 1);
}

function genValue(rng: Rng, depth: number): unknown {
  const r = rng.next();
  if (depth <= 0 || r < 0.35) {
    return rng.pick([0, 1, 42, '', 'hi', true, false, null, 'Ada']);
  }
  if (r < 0.65) {
    const len = rng.pick([0, 0, 1, 2, 3]); // empty arrays are likely: exercise empty loops
    const arr: unknown[] = [];
    for (let i = 0; i < len; i++) arr.push(genValue(rng, depth - 1));
    return arr;
  }
  const obj: Record<string, unknown> = {};
  const keys = ['name', 'value', 'deep', 'cells'];
  for (const k of keys) {
    if (rng.chance(0.6)) obj[k] = genValue(rng, depth - 1);
  }
  return obj;
}

function genData(rng: Rng): Record<string, unknown> {
  const data: Record<string, unknown> = {};
  for (const name of VAR_NAMES.concat(LIST_NAMES)) {
    if (rng.chance(0.7)) data[name] = genValue(rng, 2);
  }
  return data;
}

interface Outcome {
  ok: boolean;
  text?: string;
  fragments?: Fragment[];
  error?: { name: string; message: string; span: unknown; callStack: unknown };
}

function runOne(mode: EngineMode, templates: Record<string, string>, data: Record<string, unknown>): Outcome {
  const engine = new TemplateEngine(mode);
  for (const [name, src] of Object.entries(templates)) engine.addTemplate(name, src);
  try {
    const result = engine.render('main', data);
    return { ok: true, text: result.text, fragments: result.fragments };
  } catch (err) {
    if (err instanceof TemplateError) {
      return {
        ok: false,
        error: {
          name: err.name,
          message: err.message,
          span: err.span,
          callStack: err.callStack,
        },
      };
    }
    throw err;
  }
}

test('differential: compiler matches interpreter on random nested templates', () => {
  const iterations = Number(process.argv[2] || 2000);
  for (let i = 0; i < iterations; i++) {
    const rng = new Rng(i * 2654435761 + 1);
    const templates: Record<string, string> = { main: genTemplate(rng, true) };
    for (const p of PARTIALS) templates[p] = genTemplate(rng, false);
    const data = genData(rng);

    const expected = runOne('interpret', templates, data);
    const actual = runOne('compile', templates, data);
    try {
      assertEqual(actual, expected, `outcome mismatch at seed ${i}`);
    } catch (err) {
      console.error(`templates: ${JSON.stringify(templates)}`);
      console.error(`data: ${JSON.stringify(data)}`);
      throw err;
    }
  }
});

test('differential: targeted nesting and empty-loop cases', () => {
  const cases: Array<{ templates: Record<string, string>; data: Record<string, unknown> }> = [
    {
      templates: {
        main: '{{#each a as item}}{{#each item as item}}{{#each item as item}}{{item}}{{/each}}{{/each}}{{/each}}',
      },
      data: { a: [[['x', 'y'], []], [], [['z']]] },
    },
    {
      templates: { main: '{{#if a}}{{#if b}}{{#if c}}deep{{else}}{{d}}{{/if}}{{/if}}{{/if}}' },
      data: { a: true, b: true, c: false },
    },
    {
      templates: { main: '{{#each list as x}}{{> p}}{{/each}}', p: '{{@index}}={{x}};' },
      data: { list: [] },
    },
    {
      templates: { main: '{{#each list as x}}{{> p}}{{/each}}', p: '{{@index}}={{x}};' },
      data: { list: ['a', 'b'], x: 'outer' },
    },
    {
      templates: { main: '{{#if a}}{{else}}{{#if b}}{{else}}{{undefinedVar}}{{/if}}{{/if}}' },
      data: { a: false, b: true },
    },
    {
      templates: { main: '{{#each missingList as i}}{{/each}}' },
      data: {},
    },
  ];
  for (const [i, c] of cases.entries()) {
    const expected = runOne('interpret', c.templates, c.data);
    const actual = runOne('compile', c.templates, c.data);
    assertEqual(actual, expected, `targeted case ${i}`);
  }
});
