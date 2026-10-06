import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  compileRouter,
  crossCheckConflicts,
  enumeratePaths,
} from "../src/index.ts";

function compile(patterns: string[]) {
  return compileRouter(patterns.map((pattern, i) => ({ pattern, handler: i })));
}

describe("enumeratePaths", () => {
  it("enumerates all sequences up to maxLength", () => {
    const paths = enumeratePaths(["a", "b"], 2);
    assert.equal(paths.length, 1 + 2 + 4);
    assert.deepEqual(paths[0], []);
  });
});

describe("crossCheckConflicts: enumeration vs unification", () => {
  const tables: string[][] = [
    ["/:a/x", "/:b/x"],
    ["/a(/b)", "/a/b"],
    ["/f/*rest", "/f/:id"],
    ["/x(/y)(/z)", "/x(/z)/y"],
    ["/users/:id", "/users/new", "/users(/:id)/posts", "/*all"],
    ["/", "/a", "/a/:b", "/a/b/c", "/files/*path"],
    ["/a(/b)(/c)", "/a/b(/c)", "/a(/b)/c"],
    ["/:a", "/:a/:b", "/:a/:b/:c", "/*rest"],
  ];

  for (const patterns of tables) {
    it(`agrees with runtime for ${JSON.stringify(patterns)}`, () => {
      const router = compile(patterns);
      assert.deepEqual(crossCheckConflicts(router, { maxLength: 4 }), []);
    });
  }

  it("agrees on seeded random tables", () => {
    let state = 0x2f6e2b1;
    const rand = (n: number): number => {
      state = (state * 1103515245 + 12345) & 0x7fffffff;
      return state % n;
    };
    const randomPattern = (): string => {
      const names = ["x", "y", "z"];
      let nameAt = 0;
      const seg = (): string => {
        const roll = rand(4);
        if (roll === 0) return rand(2) === 0 ? "a" : "b";
        if (roll <= 2) return `:${names[nameAt++ % 3]}`;
        return "c";
      };
      const count = 1 + rand(3);
      const parts: string[] = [];
      for (let i = 0; i < count; i++) {
        let s = seg();
        if (rand(4) === 0) s = `(/${s})`;
        parts.push(s);
      }
      if (rand(5) === 0) parts.push("*rest");
      return "/" + parts.join("/");
    };
    for (let trial = 0; trial < 300; trial++) {
      const size = 2 + rand(3);
      const patterns = Array.from({ length: size }, randomPattern);
      const router = compile(patterns);
      const problems = crossCheckConflicts(router, { maxLength: 4 });
      assert.deepEqual(problems, [], `table ${JSON.stringify(patterns)}`);
    }
  });
});
