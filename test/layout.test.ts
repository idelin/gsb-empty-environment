import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  MONO_V1,
  SERIF_V1,
  STYLE_A,
  STYLE_B,
  makeNodeTree,
} from "../src/fixtures.ts";
import { LayoutEngine, type LayoutFeedback } from "../src/layout.ts";
import { FontMeasurer, FontRegistry } from "../src/measurer.ts";
import { ControllableScheduler } from "../src/scheduler.ts";
import type { LayoutNode, LayoutResult } from "../src/types.ts";

function setup(options: { maxRounds?: number; feedback?: LayoutFeedback } = {}) {
  const registry = new FontRegistry();
  registry.register(SERIF_V1);
  registry.register(MONO_V1);
  const scheduler = new ControllableScheduler();
  const measurer = new FontMeasurer(registry, scheduler);
  const engine = new LayoutEngine(measurer, options);
  return { registry, scheduler, measurer, engine };
}

/** Pump the scheduler until the final layout promise settles. */
async function drive<T>(scheduler: ControllableScheduler, promise: Promise<T>): Promise<T> {
  let settled = false;
  let value: T;
  let failure: unknown;
  promise.then(
    (v) => {
      value = v;
      settled = true;
    },
    (error) => {
      failure = error;
      settled = true;
    },
  );
  while (!settled) {
    await scheduler.pumpAll();
    await new Promise((resolve) => setImmediate(resolve));
  }
  if (failure) throw failure;
  return value!;
}

describe("LayoutEngine", () => {
  it("publishes the estimate synchronously and the final separately", async () => {
    const { scheduler, engine } = setup();
    const session = engine.layout(makeNodeTree());
    assert.equal(session.estimate.channel, "estimate");
    assert.equal(session.estimate.tree.children[0]!.width, 86.4, "coarse estimate metrics");
    const final = await drive(scheduler, session.final);
    assert.equal(final.channel, "final");
    assert.equal(final.tree.children[0]!.width, 68, "precise Serif v1 metrics");
  });

  it("lays out the fixture node tree with pure-function wrapping", async () => {
    const { scheduler, engine } = setup();
    const tree: LayoutNode = {
      kind: "box",
      id: "root",
      width: 40,
      children: [
        { kind: "text", id: "long", text: "abcd abcd abcd", style: STYLE_A },
        { kind: "text", id: "mono", text: "xyz", style: STYLE_B },
      ],
    };
    const session = engine.layout(tree);
    const final = await drive(scheduler, session.final);
    const [long, mono] = final.tree.children;
    assert.equal(long!.lines, 3, "greedy cluster wrapping, no browser engine");
    assert.equal(long!.height, 3 * 19.2);
    assert.equal(mono!.lines, 1);
    assert.equal(final.tree.height, long!.height + mono!.height);
  });

  it("dedupes feedback requests by key so converging feedback terminates", async () => {
    const { scheduler, measurer, engine } = setup({
      feedback: () => [{ text: "zz", style: STYLE_B }],
    });
    const session = engine.layout(makeNodeTree());
    const final: LayoutResult = await drive(scheduler, session.final);
    assert.equal(final.round, 2, "one settle round after the first");
    assert.equal(measurer.stats.tasksStarted, 4, "t1, t2, t3 plus one feedback key");
  });

  it("bounds hostile layout feedback so it cannot loop forever", async () => {
    const { scheduler, measurer, engine } = setup({
      maxRounds: 3,
      // Every round asks for a brand-new key: without a round cap this spins.
      feedback: (_tree, round) => [
        { text: "abcd", style: { ...STYLE_A, letterSpacing: round } },
      ],
    });
    const session = engine.layout(makeNodeTree());
    const final = await drive(scheduler, session.final);
    assert.equal(final.round, 3, "loop stops at the round cap");
    assert.equal(measurer.stats.tasksStarted, 6, "3 node texts + 1 hostile key per round");
  });
});
