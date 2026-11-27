import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  collectFacts,
  extractIndexPlan,
  GinPathIndex,
  JsonPathError,
  JsonPathEvaluator,
  parseJsonPath,
  type JsonPathAst,
  type JsonValue,
} from '../src/index.ts';

class Rng {
  private state: number;

  constructor(seed: number) {
    this.state = seed >>> 0;
  }

  next(): number {
    let x = this.state;
    x ^= x << 13;
    x ^= x >>> 17;
    x ^= x << 5;
    this.state = x >>> 0;
    return this.state / 0xffffffff;
  }

  int(maxExclusive: number): number {
    return Math.floor(this.next() * maxExclusive);
  }

  pick<T>(items: readonly T[]): T {
    return items[this.int(items.length)];
  }
}

const KEYS = ['a', 'b', 'c', 'id', 'name', 'nested', 'items', 'flag', 'score'] as const;
const STRING_VALUES = ['alpha', 'beta', 'gamma', 'x', 'y'];
const NUMBER_VALUES = [0, 1, 5, 10, 42, 99, -3, 2.5];

function randomValue(rng: Rng, depth: number): JsonValue {
  if (depth <= 0 || rng.next() < 0.4) return randomScalar(rng);
  if (rng.next() < 0.45) {
    return Array.from({ length: rng.int(4) }, () => randomValue(rng, depth - 1));
  }
  const object: Record<string, JsonValue> = {};
  for (let i = 0; i < 1 + rng.int(3); i += 1) {
    object[rng.pick(KEYS)] = randomValue(rng, depth - 1);
  }
  return object;
}

function randomScalar(rng: Rng): JsonValue {
  switch (rng.int(5)) {
    case 0:
      return null;
    case 1:
      return rng.next() < 0.5;
    case 2:
      return rng.pick(NUMBER_VALUES);
    case 3:
      return rng.pick(STRING_VALUES);
    default:
      return rng.int(50);
  }
}

function randomPathExpression(rng: Rng, depth: number): string {
  const parts: string[] = ['$'];
  const stepCount = rng.int(Math.min(depth, 4)) + 1;
  for (let i = 0; i < stepCount; i += 1) {
    const choice = rng.int(7);
    if (choice === 0) parts.push('.*');
    else if (choice === 1) parts.push('.**');
    else if (choice === 2) parts.push('[*]');
    else if (choice === 3) parts.push(`[${rng.int(5) - 1}]`);
    else if (choice === 4) parts.push('[0 to 2]');
    else if (choice === 5 && parts.length > 1) parts.push(` ? (${randomPredicate(rng)})`);
    else parts.push(`.${rng.pick(KEYS)}`);
  }
  if (rng.next() < 0.2) parts.push(` ? (${randomPredicate(rng)})`);
  return parts.join('');
}

function randomPredicate(rng: Rng): string {
  const atom = (): string => {
    const choice = rng.int(6);
    if (choice === 0) return `@.${rng.pick(KEYS)} == ${rng.pick(NUMBER_VALUES)}`;
    if (choice === 1) return `@.${rng.pick(KEYS)} == "${rng.pick(STRING_VALUES)}"`;
    if (choice === 2) return `@.${rng.pick(KEYS)} >= ${rng.pick(NUMBER_VALUES)}`;
    if (choice === 3) return `exists(@.${rng.pick(KEYS)})`;
    if (choice === 4) return `@.${rng.pick(KEYS)} != ${rng.pick(NUMBER_VALUES)}`;
    if (choice === 5) return `@.${rng.pick(KEYS)} like_regex "a"`;
    return 'true';
  };
  const left = atom();
  const connector = rng.pick(['&&', '||']);
  const right = rng.next() < 0.4 ? `!(${atom()})` : atom();
  if (rng.next() < 0.3) return `!(${left})`;
  return `(${left} ${connector} ${right})`;
}

describe('adversarial random-expression recall', () => {
  const rng = new Rng(0xa11ce);
  const documents: JsonValue[] = Array.from({ length: 250 }, () => randomValue(rng, 5));
  const index = new GinPathIndex();
  documents.forEach((doc, id) => index.addDocument(id, doc));

  const expressions: { source: string; ast: JsonPathAst }[] = [];
  for (let i = 0; i < 240; i += 1) {
    const mode = rng.next() < 0.5 ? 'lax' : 'strict';
    const source = `${mode} ${randomPathExpression(rng, 4)}`;
    try {
      expressions.push({ source, ast: parseJsonPath(source) });
    } catch (error) {
      assert.ok(error instanceof JsonPathError);
    }
  }

  it('every evaluator match survives the index pre-filter', () => {
    assert.ok(expressions.length >= 120);
    let exercisedNonFullScan = 0;
    let totalMatches = 0;

    for (const { source, ast } of expressions) {
      const plan = extractIndexPlan(ast);
      if (!plan.fullScan) exercisedNonFullScan += 1;
      const candidates = new Set(index.query(plan));
      const evaluator = new JsonPathEvaluator(ast);

      for (let id = 0; id < documents.length; id += 1) {
        let hit = false;
        try {
          hit = evaluator.match(documents[id]);
        } catch (error) {
          assert.ok(error instanceof JsonPathError, `unexpected error type for ${source}`);
          continue;
        }
        if (hit) {
          totalMatches += 1;
          assert.ok(candidates.has(id), `FALSE NEGATIVE doc ${id} under ${source}`);
        }
      }
    }

    assert.ok(exercisedNonFullScan >= 20, 'expected many selective plans');
    assert.ok(totalMatches > 0, 'expected the corpus to produce matches');
  });

  it('full-scan plans return the entire corpus', () => {
    const fullScanExpressions = expressions.filter(
      ({ ast }) => extractIndexPlan(ast).fullScan,
    );
    assert.ok(fullScanExpressions.length > 0);
    for (const { ast } of fullScanExpressions) {
      assert.equal(index.query(extractIndexPlan(ast)).length, documents.length);
    }
  });

  it('never emits an index fact with an array position', () => {
    for (const doc of documents.slice(0, 25)) {
      for (const fact of collectFacts(doc)) {
        assert.ok(!fact.keyPath.some((segment) => /^\d+$/.test(segment)));
      }
    }
  });
});
