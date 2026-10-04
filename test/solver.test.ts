import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { InputError, parseProblem } from "../src/model.ts";
import { solve } from "../src/solver.ts";
import type { Problem } from "../src/model.ts";

function problem(
  links: string[],
  observations: Array<[string, string[], "ok" | "fail"]>,
): Problem {
  return {
    links,
    observations: observations.map(([id, path, status]) => ({
      id,
      path,
      status,
    })),
  };
}

describe("satisfiable inputs", () => {
  it("finds the single shared faulty edge", () => {
    const result = solve(
      problem(["e1", "e2", "e3"], [
        ["p1", ["e1", "e2"], "fail"],
        ["p2", ["e2", "e3"], "fail"],
      ]),
    );
    assert.deepEqual(result, {
      status: "ok",
      minFaults: 1,
      solutions: [["e2"]],
    });
  });

  it("enumerates all tied minimum solutions", () => {
    const result = solve(
      problem(["e1", "e2"], [
        ["p1", ["e1", "e2"], "fail"],
      ]),
    );
    assert.deepEqual(result, {
      status: "ok",
      minFaults: 1,
      solutions: [["e1"], ["e2"]],
    });
  });

  it("handles disjoint failing paths requiring multiple faults", () => {
    const result = solve(
      problem(["e1", "e2", "e3", "e4"], [
        ["p1", ["e1", "e2"], "fail"],
        ["p2", ["e3", "e4"], "fail"],
      ]),
    );
    assert.equal(result.status, "ok");
    if (result.status !== "ok") return;
    assert.equal(result.minFaults, 2);
    assert.deepEqual(result.solutions, [
      ["e1", "e3"],
      ["e1", "e4"],
      ["e2", "e3"],
      ["e2", "e4"],
    ]);
  });

  it("respects healthy edges forced by ok observations", () => {
    const result = solve(
      problem(["e1", "e2", "e3"], [
        ["good", ["e1", "e2"], "ok"],
        ["bad", ["e1", "e2", "e3"], "fail"],
      ]),
    );
    assert.deepEqual(result, {
      status: "ok",
      minFaults: 1,
      solutions: [["e3"]],
    });
  });

  it("returns the empty fault set when nothing fails", () => {
    const result = solve(
      problem(["e1"], [["p1", ["e1"], "ok"]]),
    );
    assert.deepEqual(result, {
      status: "ok",
      minFaults: 0,
      solutions: [[]],
    });
  });

  it("deduplicates equivalent minimum solutions", () => {
    const result = solve(
      problem(["a", "b", "c"], [
        ["p1", ["a", "b"], "fail"],
        ["p2", ["b", "c"], "fail"],
        ["p3", ["a", "c"], "fail"],
      ]),
    );
    assert.equal(result.status, "ok");
    if (result.status !== "ok") return;
    assert.equal(result.minFaults, 2);
    assert.deepEqual(result.solutions, [
      ["a", "b"],
      ["a", "c"],
      ["b", "c"],
    ]);
  });
});

describe("contradictory inputs", () => {
  it("reports a single-observation core for an empty failing path", () => {
    const result = solve(
      problem(["e1"], [["p1", [], "fail"]]),
    );
    assert.deepEqual(result, {
      status: "contradiction",
      minUnsatObservationSubsets: [["p1"]],
    });
  });

  it("reports the failing probe plus the single covering ok probe", () => {
    const result = solve(
      problem(["e1", "e2"], [
        ["good", ["e1", "e2"], "ok"],
        ["bad", ["e1", "e2"], "fail"],
      ]),
    );
    assert.deepEqual(result, {
      status: "contradiction",
      minUnsatObservationSubsets: [["bad", "good"]],
    });
  });

  it("finds the minimum set of ok probes covering the failing path", () => {
    const result = solve(
      problem(["e1", "e2", "e3"], [
        ["ok1", ["e1"], "ok"],
        ["ok2", ["e2", "e3"], "ok"],
        ["ok3", ["e1", "e2", "e3"], "ok"],
        ["bad", ["e1", "e2", "e3"], "fail"],
      ]),
    );
    assert.deepEqual(result, {
      status: "contradiction",
      minUnsatObservationSubsets: [["bad", "ok3"]],
    });
  });

  it("enumerates all minimum cores when several exist", () => {
    const result = solve(
      problem(["e1", "e2"], [
        ["ok1", ["e1"], "ok"],
        ["ok2", ["e2"], "ok"],
        ["ok3", ["e1", "e2"], "ok"],
        ["bad", ["e1", "e2"], "fail"],
      ]),
    );
    assert.deepEqual(result, {
      status: "contradiction",
      minUnsatObservationSubsets: [["bad", "ok3"]],
    });
  });

  it("combines multiple ok probes when one is not enough", () => {
    const result = solve(
      problem(["e1", "e2"], [
        ["ok1", ["e1"], "ok"],
        ["ok2", ["e2"], "ok"],
        ["bad", ["e1", "e2"], "fail"],
      ]),
    );
    assert.deepEqual(result, {
      status: "contradiction",
      minUnsatObservationSubsets: [["bad", "ok1", "ok2"]],
    });
  });

  it("picks the smallest core across several failing probes", () => {
    const result = solve(
      problem(["e1", "e2", "e3"], [
        ["ok1", ["e1"], "ok"],
        ["ok2", ["e2"], "ok"],
        ["ok3", ["e3"], "ok"],
        ["bad1", ["e1", "e2", "e3"], "fail"],
        ["bad2", ["e3"], "fail"],
      ]),
    );
    assert.deepEqual(result, {
      status: "contradiction",
      minUnsatObservationSubsets: [["bad2", "ok3"]],
    });
  });
});

describe("input validation", () => {
  it("rejects more than 25 links", () => {
    const links = Array.from({ length: 26 }, (_, i) => `e${i}`);
    assert.throws(
      () => parseProblem({ topology: { links }, observations: [] }),
      InputError,
    );
  });

  it("rejects unknown links in paths", () => {
    assert.throws(
      () =>
        parseProblem({
          topology: { links: ["e1"] },
          observations: [{ id: "p1", path: ["e9"], status: "fail" }],
        }),
      InputError,
    );
  });

  it("accepts link objects with endpoints", () => {
    const parsed = parseProblem({
      topology: { links: [{ id: "e1", from: "A", to: "B" }] },
      observations: [{ id: "p1", path: ["e1"], status: "fail" }],
    });
    assert.deepEqual(parsed.links, ["e1"]);
  });

  it("rejects invalid observation status", () => {
    assert.throws(
      () =>
        parseProblem({
          topology: { links: ["e1"] },
          observations: [{ id: "p1", path: ["e1"], status: "flaky" }],
        }),
      InputError,
    );
  });
});
