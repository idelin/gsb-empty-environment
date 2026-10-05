import { strict as assert } from "assert";
import { solve } from "../src/solver";
import { PlanOk, SolverInput } from "../src/types";

let passed = 0;
function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok - ${name}`);
  } catch (err) {
    console.error(`FAIL - ${name}`);
    console.error(err);
    process.exitCode = 1;
  }
}

function ok(input: SolverInput): PlanOk {
  const r = solve(input);
  assert.equal(r.status, "ok", JSON.stringify(r));
  return r as PlanOk;
}

function versionOf(r: PlanOk, name: string): number | undefined {
  return r.plan.find((e) => e.name === name)?.version;
}

// 1. Highest version inside the range is chosen.
test("picks highest version in range", () => {
  const r = ok({
    repository: [
      { name: "foo", versions: [{ version: 1 }, { version: 2 }, { version: 3 }, { version: 4 }] },
    ],
    request: [{ name: "foo", range: ">=2 <4" }],
  });
  assert.equal(versionOf(r, "foo"), 3);
  assert.equal(r.changes, 1); // new install counts as one change
});

// 2. Installed version is kept when it satisfies the request (zero changes).
test("keeps installed version when possible", () => {
  const r = ok({
    repository: [
      { name: "foo", versions: [{ version: 1 }, { version: 2 }, { version: 3 }] },
    ],
    installed: { foo: 2 },
    request: [{ name: "foo", range: ">=1" }],
  });
  assert.equal(versionOf(r, "foo"), 2);
  assert.equal(r.changes, 0);
  assert.equal(r.plan[0].change, "keep");
});

// 3. Forced upgrade: minimal number of upgrades, then highest version wins.
test("minimal upgrades, highest version on tie", () => {
  const r = ok({
    repository: [
      { name: "foo", versions: [{ version: 1 }, { version: 2 }, { version: 3 }] },
      {
        name: "bar",
        versions: [
          { version: 1, dependencies: [{ name: "foo", range: ">=2" }] },
        ],
      },
    ],
    installed: { foo: 1 },
    request: [{ name: "bar", range: "1" }, { name: "foo", range: ">=1" }],
  });
  assert.equal(versionOf(r, "foo"), 3); // 2 and 3 both cost one upgrade; 3 wins
  assert.equal(r.changes, 2); // foo upgraded, bar newly installed
});

// 4. Optional feature pulls its dependency only when enabled.
test("optional features gate dependencies", () => {
  const repo = [
    {
      name: "app",
      versions: [
        {
          version: 1,
          features: { ssl: [{ name: "openssl", range: ">=1" }] },
        },
      ],
    },
    { name: "openssl", versions: [{ version: 1 }, { version: 2 }] },
  ];
  const off = ok({ repository: repo, request: [{ name: "app" }] });
  assert.equal(versionOf(off, "openssl"), undefined);
  const on = ok({
    repository: repo,
    request: [{ name: "app", features: ["ssl"] }],
  });
  assert.equal(versionOf(on, "openssl"), 2);
  assert.deepEqual(on.plan.find((e) => e.name === "app")?.features, ["ssl"]);
});

// 5. Dependency can enable a feature on the target package.
test("dependency enables target features", () => {
  const r = ok({
    repository: [
      {
        name: "app",
        versions: [
          { version: 1, dependencies: [{ name: "db", range: ">=1", features: ["tls"] }] },
        ],
      },
      {
        name: "db",
        versions: [
          { version: 1, features: { tls: [{ name: "cert", range: ">=1" }] } },
        ],
      },
      { name: "cert", versions: [{ version: 5 }] },
    ],
    request: [{ name: "app" }],
  });
  assert.equal(versionOf(r, "cert"), 5);
});

// 6. Cross-package conflict forbids the combination.
test("cross-package conflict is respected", () => {
  const r = ok({
    repository: [
      {
        name: "foo",
        versions: [
          { version: 1 },
          { version: 2, conflicts: [{ name: "bar", range: ">=3" }] },
        ],
      },
      { name: "bar", versions: [{ version: 3 }] },
    ],
    request: [{ name: "foo" }, { name: "bar" }],
  });
  assert.equal(versionOf(r, "foo"), 1); // foo@2 would clash with bar@3
  assert.equal(versionOf(r, "bar"), 3);
});

// 7. Unsatisfiable: the conflict core contains both culprits, not just the last one.
test("conflict core is a verifiable minimal subset", () => {
  const input: SolverInput = {
    repository: [
      { name: "a", versions: [{ version: 1, dependencies: [{ name: "c", range: ">=2" }] }] },
      { name: "b", versions: [{ version: 1, dependencies: [{ name: "c", range: "<2" }] }] },
      { name: "c", versions: [{ version: 1 }, { version: 2 }] },
      { name: "unrelated", versions: [{ version: 1 }] },
    ],
    request: [{ name: "a" }, { name: "b" }, { name: "unrelated" }],
  };
  const r = solve(input);
  assert.equal(r.status, "conflict");
  if (r.status !== "conflict") return;
  const names = r.core.map((c) => c.name).sort();
  assert.deepEqual(names, ["a", "b"]); // "unrelated" must not be in the core
  // Minimality: every proper subset of the core is satisfiable.
  for (const drop of r.core) {
    const sub = solve({ ...input, request: r.core.filter((c) => c !== drop) });
    assert.equal(sub.status, "ok", `core not minimal after dropping ${drop.name}`);
  }
  assert.ok(r.explanation.some((l) => l.includes("a@1 depends on c >=2")));
  assert.ok(r.explanation.some((l) => l.includes("b@1 depends on c <2")));
});

// 8. Single unsatisfiable request is reported on its own.
test("unknown package yields a singleton core", () => {
  const r = solve({
    repository: [{ name: "foo", versions: [{ version: 1 }] }],
    request: [{ name: "foo" }, { name: "ghost" }],
  });
  assert.equal(r.status, "conflict");
  if (r.status !== "conflict") return;
  assert.deepEqual(r.core, [{ name: "ghost" }]);
  assert.ok(r.explanation.some((l) => l.includes("ghost: not found in repository")));
});

// 9. Feature-induced conflict shows up in the core.
test("conflict through optional feature", () => {
  const r = solve({
    repository: [
      {
        name: "app",
        versions: [
          { version: 1, features: { ssl: [{ name: "libssl", range: ">=3" }] } },
        ],
      },
      { name: "libssl", versions: [{ version: 1 }, { version: 3 }] },
      {
        name: "scanner",
        versions: [{ version: 1, conflicts: [{ name: "libssl", range: ">=2" }] }],
      },
    ],
    request: [{ name: "app", features: ["ssl"] }, { name: "scanner" }],
  });
  assert.equal(r.status, "conflict");
  if (r.status !== "conflict") return;
  assert.deepEqual(r.core.map((c) => c.name).sort(), ["app", "scanner"]);
});

// 10. Twenty-package chain solves quickly and picks highest versions.
test("twenty package chain", () => {
  const repository = [];
  for (let i = 0; i < 20; i++) {
    const deps = i === 0 ? [] : [{ name: `p${i - 1}`, range: ">=1" }];
    repository.push({
      name: `p${i}`,
      versions: [
        { version: 1, dependencies: deps },
        { version: 2, dependencies: deps },
      ],
    });
  }
  const r = ok({ repository, request: [{ name: "p19" }] });
  assert.equal(r.plan.length, 20);
  for (const e of r.plan) assert.equal(e.version, 2);
});

// 11. Downgrade is possible when required, and counted.
test("downgrade when no other option", () => {
  const r = ok({
    repository: [
      { name: "foo", versions: [{ version: 1 }, { version: 2 }] },
      { name: "bar", versions: [{ version: 1, dependencies: [{ name: "foo", range: "<2" }] }] },
    ],
    installed: { foo: 2 },
    request: [{ name: "bar" }, { name: "foo" }],
  });
  assert.equal(versionOf(r, "foo"), 1);
  assert.equal(r.plan.find((e) => e.name === "foo")?.change, "downgrade");
});

// 12. Avoids pulling an unneeded extra package (it would cost a change).
test("no unnecessary packages installed", () => {
  const r = ok({
    repository: [
      { name: "foo", versions: [{ version: 1 }] },
      { name: "extra", versions: [{ version: 1 }] },
    ],
    request: [{ name: "foo" }],
  });
  assert.equal(r.plan.length, 1);
});

console.log(`\n${passed} tests passed`);
