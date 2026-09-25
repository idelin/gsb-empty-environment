import { FixEngine, type EngineReport } from "./engine.ts";
import type { JsonValue } from "./json.ts";
import { coerceBooleanStrings, rewriteValue, trimStrings } from "./rules.ts";

function show(title: string, report: EngineReport, rollback?: JsonValue): void {
  console.log(`\n=== ${title} ===`);
  console.log(JSON.stringify(report, null, 2));
  if (rollback !== undefined) {
    console.log("rollback ->", JSON.stringify(rollback));
  }
}

// Scenario 1: priorities + preconditions, terminates at a fixpoint.
{
  const input = {
    features: { darkMode: "true", beta: "false" },
    name: "  alice  ",
    mode: "x",
  };
  const engine = new FixEngine(input, [
    coerceBooleanStrings("coerce-bool", 10, ["/features/darkMode", "/features/beta"]),
    trimStrings("trim-name", 5, ["/name"]),
    rewriteValue("rename-x-to-y", 1, "/mode", "x", "y"),
  ]);
  const report = engine.run();
  show("1. fixpoint (priority order: coerce-bool > trim-name > rename)", report);
}

// Scenario 2: rule A rewrites x->y, rule B rewrites y->x => oscillation.
{
  const input = { mode: "x" };
  const engine = new FixEngine(input, [
    rewriteValue("A:x-to-y", 10, "/mode", "x", "y"),
    rewriteValue("B:y-to-x", 10, "/mode", "y", "x"),
  ]);
  const report = engine.run();
  show("2. oscillation A<->B detected and stopped", report, engine.rollback());
}

// Scenario 3: two same-priority rules disagree on one path => conflict.
{
  const input = { theme: "light" };
  const engine = new FixEngine(input, [
    rewriteValue("set-theme-dark", 10, "/theme", "light", "dark"),
    rewriteValue("set-theme-blue", 10, "/theme", "light", "blue"),
  ]);
  const report = engine.run();
  show("3. conflict on /theme, engine stops with evidence", report, engine.rollback());
}

// Scenario 4: an ever-applicable rule never reaches a fixpoint; the step
// budget forces termination. Rollback still restores the exact input.
{
  const input = { counter: 0, keep: [1, 2, 3] };
  const engine = new FixEngine(
    input,
    [
      {
        id: "increment-counter",
        priority: 10,
        match(state) {
          const n = (state as { counter: number }).counter;
          return [{ path: "/counter", expect: n, replacement: n + 1 }];
        },
      },
    ],
    { maxSteps: 3 },
  );
  const report = engine.run();
  show("4. budget termination after 3 steps", report, engine.rollback());
}
