import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ConflictError,
  Router,
  enumerateConflicts,
  type CompiledRoute,
  type Conflict,
} from "../src/index.ts";
import { parsePattern, expand } from "../src/pattern.ts";

function compile(paths: string[]): CompiledRoute[] {
  return paths.map((source, index) => ({
    source,
    index,
    variants: expand(parsePattern(source)),
  }));
}

function pairSet(conflicts: Conflict[]): Set<string> {
  return new Set(conflicts.map((c) => `${c.routes[0]}:${c.routes[1]}`));
}

test("identical patterns conflict with a witness", () => {
  const router = new Router<string>([
    { path: "/a/:id", data: "one" },
    { path: "/a/:name", data: "two" },
  ]);
  const conflicts = router.conflicts();
  assert.equal(conflicts.length, 1);
  assert.deepEqual(conflicts[0]!.routes, [0, 1]);
  assert.equal(conflicts[0]!.witness, "/a/x");
  assert.deepEqual(conflicts[0]!.priority, { literalCount: 1, omittedCount: 0 });
});

test("Router.compile throws ConflictError on ties", () => {
  assert.throws(
    () => Router.compile([{ path: "/a/:id" }, { path: "/a/:name" }]),
    (error: unknown) => {
      assert.ok(error instanceof ConflictError);
      assert.match(error.message, /witness \/a\/x/);
      return true;
    },
  );
});

test("different literal counts never conflict", () => {
  const router = new Router<string>([
    { path: "/a/:x", data: "param" },
    { path: "/a/b", data: "literal" },
  ]);
  assert.deepEqual(router.conflicts(), []);
});

test("optional group expansion can tie with a literal route", () => {
  const router = new Router<string>([
    { path: "/a(/b)", data: "optional" },
    { path: "/a/b", data: "literal" },
  ]);
  const conflicts = router.conflicts();
  assert.equal(conflicts.length, 1);
  assert.equal(conflicts[0]!.witness, "/a/b");
});

test("optional omission counts differ, so no conflict", () => {
  const router = new Router<string>([
    { path: "/a(/b)", data: "optional" },
    { path: "/a", data: "plain" },
  ]);
  assert.deepEqual(router.conflicts(), []);
});

test("wildcard and param routes tie on single-segment paths", () => {
  const router = new Router<string>([
    { path: "/a/*rest", data: "wild" },
    { path: "/a/:x", data: "param" },
  ]);
  const conflicts = router.conflicts();
  assert.equal(conflicts.length, 1);
  assert.equal(conflicts[0]!.witness, "/a/x");
});

test("reported witnesses are genuine ties", () => {
  const paths = ["/a(/b)/:p", "/a/:q/c", "/a/b/*rest", "(/x)/a/:y"];
  const router = new Router<string>(paths.map((path) => ({ path, data: path })));
  for (const conflict of router.conflicts()) {
    const matched = router.matchAll(conflict.witness);
    const tied = matched.filter(
      (m) =>
        m.literalCount === conflict.priority.literalCount &&
        m.omittedCount === conflict.priority.omittedCount,
    );
    const indices = tied.map((m) => m.route.index);
    assert.ok(indices.includes(conflict.routes[0]), conflict.witness);
    assert.ok(indices.includes(conflict.routes[1]), conflict.witness);
  }
});

function mulberry32(seed: number): () => number {
  let state = seed;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomPattern(rand: () => number): string {
  const segmentCount = 1 + Math.floor(rand() * 3);
  const parts: string[] = [];
  for (let i = 0; i < segmentCount; i++) {
    const roll = rand();
    let segment: string;
    if (roll < 0.45) segment = rand() < 0.5 ? "a" : "b";
    else if (roll < 0.85) segment = rand() < 0.5 ? ":p" : ":q";
    else segment = "*r";
    if (segment === "*r" && i !== segmentCount - 1) segment = "a";
    if (rand() < 0.25 && segment !== "*r") {
      parts.push(`(/${segment})`);
    } else {
      parts.push(`/${segment}`);
    }
  }
  let source = parts.join("");
  // dedupe repeated param/wildcard names by rewriting later occurrences
  const seen = new Set<string>();
  source = source.replace(/[:*]([pq r])/g, (whole, name: string) => {
    const kind = whole[0]!;
    let candidate = name.trim();
    while (seen.has(kind + candidate)) candidate += "2";
    seen.add(kind + candidate);
    return kind + candidate;
  });
  return source;
}

test("unification-based conflicts match brute-force path enumeration", () => {
  const rand = mulberry32(20261007);
  for (let trial = 0; trial < 400; trial++) {
    const routeCount = 2 + Math.floor(rand() * 3);
    const paths: string[] = [];
    while (paths.length < routeCount) {
      const candidate = randomPattern(rand);
      if (!paths.includes(candidate)) paths.push(candidate);
    }
    const routes = compile(paths);
    const maxVariantLength = Math.max(
      ...routes.flatMap((r) => r.variants.map((v) => v.segments.length)),
    );
    const expected = pairSet(enumerateConflicts(routes, Math.min(maxVariantLength + 1, 5)));
    const actual = pairSet(new Router(paths.map((path) => ({ path }))).conflicts());
    assert.deepEqual(actual, expected, `paths: ${JSON.stringify(paths)}`);
  }
});
