import { VirtualHost } from "../src/host.ts";
import { ResourceTracker } from "../src/resources.ts";
import { Renderer } from "../src/renderer.ts";
import type { TreeSpec } from "../src/renderer.ts";
import { Stepper } from "../src/stepper.ts";

let failures = 0;

function check(name: string, cond: boolean, detail = ""): void {
  if (cond) {
    console.log(`  ok   ${name}`);
  } else {
    failures++;
    console.log(`  FAIL ${name}${detail ? ` — ${detail}` : ""}`);
  }
}

function countNodes(spec: TreeSpec): number {
  return 1 + (spec.children ?? []).reduce((n, c) => n + countNodes(c), 0);
}

function expectedSnapshot(spec: TreeSpec): string {
  const kids = (spec.children ?? []).map(expectedSnapshot).join(",");
  return `${spec.type}(${spec.label ?? ""})[${kids}]`;
}

interface Fixture {
  host: VirtualHost;
  tracker: ResourceTracker;
  renderer: Renderer;
  stepper: Stepper;
}

function fixture(): Fixture {
  const host = new VirtualHost();
  const tracker = new ResourceTracker();
  const renderer = new Renderer(host, tracker);
  return { host, tracker, renderer, stepper: new Stepper(renderer) };
}

function checkResourceIntegrity(tracker: ResourceTracker, expectLive: number): void {
  check("no double-dispose", tracker.overDisposed.length === 0,
    `overDisposed=${tracker.overDisposed.join(",")}`);
  check(`live subscriptions == ${expectLive}`, tracker.live.size === expectLive,
    `live=${tracker.live.size} created=${tracker.totalCreated} disposed=${tracker.totalDisposed}`);
  check("created == disposed + live",
    tracker.totalCreated === tracker.totalDisposed + tracker.live.size);
}

const treeA: TreeSpec = {
  type: "view", label: "A0",
  children: [
    { type: "text", label: "A1" },
    { type: "view", label: "A2", children: [
      { type: "text", label: "A3" },
      { type: "image", label: "A4" },
    ]},
  ],
};

const treeB: TreeSpec = {
  type: "view", label: "B0",
  children: [
    { type: "button", label: "B1" },
    { type: "text", label: "B2" },
  ],
};

const treeC: TreeSpec = { type: "text", label: "C0" };

// 1. Basic: a complete prepare commits and becomes visible.
{
  console.log("scenario 1: basic commit");
  const { host, tracker, renderer, stepper } = fixture();
  renderer.schedule(1, treeA);
  check("not committed before stepping", renderer.committedVersion === 0);
  stepper.drain();
  check("committed v1", renderer.committedVersion === 1);
  check("visible tree matches spec", host.snapshot() === `#root()[${expectedSnapshot(treeA)}]`,
    host.snapshot());
  check("exactly one visible mutation (atomic swap)", host.visibleMutations === 1,
    `visibleMutations=${host.visibleMutations}`);
  checkResourceIntegrity(tracker, countNodes(treeA));
}

// 2. Prepare never touches the visible tree before commit.
{
  console.log("scenario 2: no visible mutation during prepare");
  const { host, tracker, renderer, stepper } = fixture();
  renderer.schedule(1, treeA);
  stepper.step();
  stepper.step();
  check("visible mutations == 0 mid-prepare", host.visibleMutations === 0);
  check("visible tree still empty", host.snapshot() === "#root()[]");
  check("partial subscriptions are live", tracker.live.size === 2);
  stepper.drain();
  check("visible mutations == 1 after commit", host.visibleMutations === 1);
  checkResourceIntegrity(tracker, countNodes(treeA));
}

// 3. Supersede mid-prepare: stale subscriptions cleaned, latest wins.
{
  console.log("scenario 3: supersede mid-prepare");
  const { host, tracker, renderer, stepper } = fixture();
  renderer.schedule(1, treeA);
  stepper.step();
  stepper.step();
  check("2 subs live from v1 prepare", tracker.live.size === 2);
  renderer.schedule(2, treeB);
  check("stale prepare subs cleaned immediately", tracker.live.size === 0);
  check("prepare session replaced", renderer.preparingVersion === null);
  stepper.drain();
  check("committed v2, not v1", renderer.committedVersion === 2);
  check("visible tree is v2", host.snapshot() === `#root()[${expectedSnapshot(treeB)}]`,
    host.snapshot());
  check("only one visible mutation total", host.visibleMutations === 1);
  checkResourceIntegrity(tracker, countNodes(treeB));
}

// 4. Exhaustive interleave replay: pause v1 at every possible point,
//    then supersede with v2. Invariants must hold for every split.
{
  console.log("scenario 4: exhaustive interleave replay");
  const splits = countNodes(treeA) + 1; // step 0..N times before superseding
  let allOk = true;
  for (let k = 0; k <= splits; k++) {
    const { host, tracker, renderer, stepper } = fixture();
    stepper.replay([
      { kind: "schedule", version: 1, spec: treeA },
      { kind: "step", count: k },
      { kind: "schedule", version: 2, spec: treeB },
      { kind: "drain" },
    ]);
    // If v1's prepare finished before v2 arrived, v1 legitimately commits
    // first (attach) and is then replaced (detach + attach): 3 mutations.
    const v1CommittedFirst = k >= countNodes(treeA);
    const expectedMutations = v1CommittedFirst ? 3 : 1;
    const ok =
      renderer.committedVersion === 2 &&
      host.snapshot() === `#root()[${expectedSnapshot(treeB)}]` &&
      host.visibleMutations === expectedMutations &&
      tracker.live.size === countNodes(treeB) &&
      tracker.overDisposed.length === 0 &&
      tracker.totalCreated === tracker.totalDisposed + tracker.live.size;
    if (!ok) {
      allOk = false;
      console.log(`  FAIL split k=${k}: committed=${renderer.committedVersion} ` +
        `live=${tracker.live.size} visibleMutations=${host.visibleMutations} ` +
        `overDisposed=${tracker.overDisposed.join(",")}`);
    }
  }
  check(`all ${splits + 1} interleavings preserve invariants`, allOk);
}

// 5. Sequential commits: old tree's subscriptions disposed exactly once.
{
  console.log("scenario 5: sequential commits replace visible tree");
  const { host, tracker, renderer, stepper } = fixture();
  stepper.replay([
    { kind: "schedule", version: 1, spec: treeA },
    { kind: "drain" },
    { kind: "schedule", version: 2, spec: treeB },
    { kind: "drain" },
    { kind: "schedule", version: 3, spec: treeC },
    { kind: "drain" },
  ]);
  check("committed v3", renderer.committedVersion === 3);
  check("visible tree is v3", host.snapshot() === `#root()[${expectedSnapshot(treeC)}]`,
    host.snapshot());
  check("live subs == v3 node count", tracker.live.size === countNodes(treeC));
  check("every replaced sub disposed exactly once",
    tracker.totalDisposed === countNodes(treeA) + countNodes(treeB) &&
    tracker.overDisposed.length === 0,
    `disposed=${tracker.totalDisposed}`);
  checkResourceIntegrity(tracker, countNodes(treeC));
}

// 6. Stale and duplicate schedules are ignored.
{
  console.log("scenario 6: stale updates ignored");
  const { tracker, renderer, stepper } = fixture();
  stepper.replay([
    { kind: "schedule", version: 2, spec: treeB },
    { kind: "drain" },
    { kind: "schedule", version: 1, spec: treeA }, // older than committed
    { kind: "schedule", version: 2, spec: treeA }, // equal to committed
  ]);
  check("no work queued for stale versions", stepper.step() === false);
  check("still committed v2", renderer.committedVersion === 2);
  check("no extra subscriptions created", tracker.totalCreated === countNodes(treeB));
  checkResourceIntegrity(tracker, countNodes(treeB));
}

// 7. Unmount cleans up exactly once, even mid-prepare and when repeated.
{
  console.log("scenario 7: unmount exactly once");
  const { host, tracker, renderer, stepper } = fixture();
  stepper.replay([
    { kind: "schedule", version: 1, spec: treeA },
    { kind: "drain" },
    { kind: "schedule", version: 2, spec: treeB },
    { kind: "step", count: 2 }, // in-flight prepare at unmount time
    { kind: "unmount" },
  ]);
  check("nothing live after unmount", tracker.live.size === 0);
  check("visible tree empty after unmount", host.snapshot() === "#root()[]");
  const disposedAfterFirst = tracker.totalDisposed;
  check("every created sub disposed exactly once",
    tracker.totalCreated === disposedAfterFirst && tracker.overDisposed.length === 0,
    `created=${tracker.totalCreated} disposed=${disposedAfterFirst}`);
  renderer.unmount(); // repeated unmount must be a no-op
  check("second unmount is a no-op",
    tracker.totalDisposed === disposedAfterFirst && tracker.overDisposed.length === 0);
  check("step after unmount is idle", stepper.step() === false);
}

console.log(failures === 0 ? "\nALL SCENARIOS PASSED" : `\n${failures} CHECK(S) FAILED`);
if (failures > 0) process.exit(1);
