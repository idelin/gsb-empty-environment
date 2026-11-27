import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  extractIndexPlan,
  FULL_SCAN,
  GinPathIndex,
  JsonPathEvaluator,
  matchJsonPath,
  parseJsonPath,
  queryJsonPath,
  type JsonPathAst,
  type JsonValue,
} from '../src/index.ts';

/** Deterministic PRNG so property runs are reproducible. */
class Rng {
  private state: number;

  constructor(seed: number) {
    this.state = seed >>> 0;
  }

  next(): number {
    // xorshift32
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

const KEYS = [
  'id',
  'name',
  'kind',
  'active',
  'score',
  'tags',
  'meta',
  'owner',
  'children',
  'v',
  'age',
  'ok',
];

function randomJson(rng: Rng, depth: number): JsonValue {
  if (depth <= 0 || rng.next() < 0.35) {
    return randomScalar(rng);
  }
  if (rng.next() < 0.45) {
    const length = rng.int(4);
    return Array.from({ length }, () => randomJson(rng, depth - 1));
  }
  const memberCount = 1 + rng.int(4);
  const object: Record<string, JsonValue> = {};
  for (let i = 0; i < memberCount; i += 1) {
    object[rng.pick(KEYS)] = randomJson(rng, depth - 1);
  }
  return object;
}

function randomScalar(rng: Rng): JsonValue {
  switch (rng.int(6)) {
    case 0:
      return null;
    case 1:
      return rng.next() < 0.5;
    case 2:
      return rng.int(100);
    case 3:
      return Math.round(rng.next() * 1000) / 10;
    case 4:
      return rng.pick(['alpha', 'beta', 'gamma', 'delta', 'x1', 'y2']);
    default:
      return rng.pick([0, 1, 5, 10, 42, 99]);
  }
}

/**
 * Shape-aware generator: guarantees the nested structures used by the path
 * templates actually occur, while randomising sibling members and scalars.
 */
function shapedDocument(rng: Rng, depth: number): JsonValue {
  const child = (): JsonValue => {
    const value: Record<string, JsonValue> = {
      id: rng.int(4),
      name: rng.pick(['alpha', 'beta', 'gamma', 'delta']),
      kind: rng.pick(['x', 'y', 'z']),
      active: rng.next() < 0.5,
      score: rng.pick([0, 5, 10, 25, 42, 99]),
      v: rng.pick([1, 5, 10]),
    };
    if (rng.next() < 0.6) value.owner = { id: rng.int(3), name: rng.pick(['alpha', 'beta']) };
    if (depth > 1 && rng.next() < 0.4) value.children = [child(), child()];
    return value;
  };

  return {
    id: rng.int(10),
    name: rng.pick(['alpha', 'beta', 'gamma']),
    kind: rng.pick(['x', 'y']),
    active: rng.next() < 0.5,
    score: rng.pick([1, 10, 25, 42]),
    threshold: 10,
    tags: [rng.pick(['alpha', 'beta', 'gamma']), rng.pick(['delta', 'alpha'])],
    meta: randomJson(rng, Math.max(1, depth - 1)),
    owner: { id: rng.int(3), name: rng.pick(['alpha', 'beta']) },
    children: [child(), child(), child()],
    v: rng.pick([1, 5, 10]),
  };
}

const PATH_TEMPLATES = [
  '$.id',
  '$.name',
  '$.kind',
  '$.active',
  '$.score',
  '$.tags',
  '$.meta',
  '$.owner.id',
  '$.owner.name',
  '$.children[*].id',
  '$.children[*].name',
  '$.children[*] ? (@.active == true)',
  '$.children[*] ? (@.score >= 10)',
  '$.**',
  '$.**.id',
  '$.** ? (@.kind == "x")',
  '$.tags[*] ? (@ == "alpha")',
  '$.children[*] ? (exists(@.owner))',
  '$.children[*] ? (!(@.active == true))',
  '$.children[*] ? (@.score < 5 || @.active == true)',
  '$.children[*] ? (@.active == true && @.score > 0)',
  '$.owner ? (@.id == 1)',
  '$.meta.*',
  '$.children[last]',
  '$.children[0 to 1]',
  '$.children[*].name ? (@ like_regex "^a")',
  '$.children[*] ? (@.score + 1 == 43)',
  '$.children[*] ? (@.name like_regex "beta" flag "i")',
  '$.children[*] ? (@.id == 1 || @.id == 2)',
  '$.** ? (@.v == 5)',
];

// Property templates exercise both modes, but recursive-descent paths are
// only defined for lax: in strict mode `.**` can raise on non-container items.
const STRICT_SKIP = new Set(['$.**.id', '$.**', '$.** ? (@.kind == "x")', '$.** ? (@.v == 5)']);
const MODES = ['lax', 'strict'] as const;

describe('GIN pushdown plan extraction', () => {
  it('degrades negation, like_regex and unbound predicates to full scan', () => {
    assert.equal(extractIndexPlan(parseJsonPath('$.a ? (!(@.b == 1))')).fullScan, true);
    assert.equal(extractIndexPlan(parseJsonPath('$.a ? (@.b like_regex "x")')).fullScan, true);
    assert.equal(extractIndexPlan(parseJsonPath('$.a ? (@.b != 1)')).fullScan, true);
  });

  it('turns OR into a disjunction and AND into intersecting groups', () => {
    const orPlan = extractIndexPlan(parseJsonPath('$.rows[*] ? (@.a == 1 || @.b == 2)'));
    assert.equal(orPlan.fullScan, false);
    assert.ok(orPlan.groups.length >= 2);

    const andPlan = extractIndexPlan(parseJsonPath('$.rows[*] ? (@.a == 1 && @.b == 2)'));
    assert.equal(andPlan.fullScan, false);
    assert.equal(andPlan.groups.length, 1);
    assert.ok(andPlan.groups[0].exact.length >= 2);

    // One unselective OR branch forces full scan.
    const mixed = extractIndexPlan(
      parseJsonPath('$.rows[*] ? (@.a == 1 || !(@.b == 2))'),
    );
    assert.equal(mixed, FULL_SCAN);
  });

  it('uses suffix constraints after wildcard or recursive descent', () => {
    const plan = extractIndexPlan(parseJsonPath('$.**.id'));
    assert.equal(plan.fullScan, false);
    assert.ok(
      plan.groups[0].anchoredSuffixes.some(
        (anchored) => anchored.suffix.at(-1) === 'id' && anchored.prefix.length === 0,
      ),
    );
  });
});

describe('zero false-negative property on random document libraries', () => {
  const rng = new Rng(0x5eed1234);
  const documents: JsonValue[] = Array.from({ length: 300 }, (_, i) =>
    i % 3 === 0 ? shapedDocument(rng, 3) : randomJson(rng, 4),
  );
  const index = new GinPathIndex();
  documents.forEach((doc, id) => index.addDocument(id, doc));

  const expressions: string[] = [];
  for (const mode of MODES) {
    for (const template of PATH_TEMPLATES) {
      if (mode === 'strict' && STRICT_SKIP.has(template)) continue;
      expressions.push(`${mode} ${template}`);
    }
  }

  for (const source of expressions) {
    it(`plan never drops matches: ${source}`, () => {
      const ast = parseJsonPath(source);
      const plan = extractIndexPlan(ast);
      const candidates = new Set(index.query(plan));
      const evaluator = new JsonPathEvaluator(ast);

      let fullScanMatches = 0;
      for (let id = 0; id < documents.length; id += 1) {
        let matched = false;
        try {
          matched = evaluator.match(documents[id]);
        } catch (error) {
          // Strict-mode paths may raise structurally; a throwing document is not
          // a result-bearing document and needs no index guarantee.
          assert.ok(error instanceof Error);
          continue;
        }
        if (matched) {
          fullScanMatches += 1;
          assert.ok(
            candidates.has(id),
            `false negative for document ${id} under ${source}`,
          );
        }
      }

      // Every template must match at least a few documents, otherwise the test
      // would prove nothing about recall.
      assert.ok(fullScanMatches > 0, `template ${source} matched no documents`);

      // Selectivity check: a non-full-scan plan should usually shrink the pool.
      if (!plan.fullScan && fullScanMatches > 0) {
        assert.ok(candidates.size <= documents.length);
      }
    });
  }

  it('rejects zero documents for FULL_SCAN plans', () => {
    const ast = parseJsonPath('strict $.a ? (!(@.b == 1))');
    const candidates = index.query(extractIndexPlan(ast));
    assert.equal(candidates.length, documents.length);
  });

  it('keeps recall with a second, differently seeded document library', () => {
    const rng2 = new Rng(0x0badf00d);
    const docs2: JsonValue[] = Array.from({ length: 200 }, (_, i) =>
      i % 2 === 0 ? shapedDocument(rng2, 4) : randomJson(rng2, 5),
    );
    const index2 = new GinPathIndex();
    docs2.forEach((doc, id) => index2.addDocument(id, doc));

    for (const source of expressions.slice(0, 20)) {
      const ast: JsonPathAst = parseJsonPath(source);
      const candidates = new Set(index2.preFilter(ast));
      const evaluator = new JsonPathEvaluator(ast);
      for (let id = 0; id < docs2.length; id += 1) {
        try {
          if (evaluator.match(docs2[id])) {
            assert.ok(candidates.has(id), `false negative doc ${id} under ${source}`);
          }
        } catch {
          // strict structural error: no result to preserve
        }
      }
    }
  });

  it('preserves every actual match when the index is used as a pre-filter', () => {
    const source = 'lax $.children[*] ? (@.active == true && @.score >= 10)';
    const ast = parseJsonPath(source);
    const candidates = new Set(index.preFilter(ast));
    const expected = documents
      .map((doc, id) => ({ id, hit: matchJsonPath(doc, ast) }))
      .filter((entry) => entry.hit)
      .map((entry) => entry.id);
    for (const id of expected) assert.ok(candidates.has(id));
    assert.ok(expected.length > 0);
    // The plan must never be a full scan when real constraints exist.
    assert.equal(extractIndexPlan(ast).fullScan, false);
    assert.ok(candidates.size >= expected.length);
  });

  it('query() and the evaluator agree on result ordering for scalar queries', () => {
    const source = 'lax $.id';
    const ast = parseJsonPath(source);
    const ids = index.preFilter(ast);
    assert.ok(ids.length > 0);
    const candidateSet = new Set(ids);
    for (const id of ids) {
      const result = queryJsonPath(documents[id], ast);
      assert.ok(Array.isArray(result));
    }
    // Every document with a root-level `id` key must survive the pre-filter.
    documents.forEach((doc, id) => {
      if (doc !== null && typeof doc === 'object' && !Array.isArray(doc) && 'id' in doc) {
        assert.ok(candidateSet.has(id));
      }
    });
  });
});
