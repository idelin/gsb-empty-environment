/**
 * Finite event exploration: drive the tree with a seeded pseudo-random event
 * stream, assert invariants after every event, and verify replay consistency
 * (same event log -> identical state snapshot).
 */

import { test } from "node:test";
import assert from "node:assert/strict";
import { TreeModel } from "../src/model.ts";
import type { TreeNode } from "../src/model.ts";
import { TreeView } from "../src/view.ts";
import { DeferredLoader, flush, lazyChildrenFixture, treeFixture } from "../src/fixtures.ts";

type Ev =
  | { k: "toggle"; id: string }
  | { k: "collapse"; id: string }
  | { k: "nav"; dir: "up" | "down" | "home" | "end" }
  | { k: "type"; ch: string }
  | { k: "clearType" }
  | { k: "select"; id: string }
  | { k: "ctrl"; id: string }
  | { k: "shift"; id: string }
  | { k: "insert"; parent: string; id: string }
  | { k: "remove"; id: string }
  | { k: "move"; id: string; parent: string }
  | { k: "filter"; q: string | null }
  | { k: "disable"; id: string; value: boolean }
  | { k: "cancelLoad"; id: string }
  | { k: "resolve"; id: string };

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function bruteForceVisible(root: TreeNode, model: TreeModel): string[] {
  const out: string[] = [];
  const walk = (node: TreeNode): void => {
    if (!model.isVisibleSelf(node)) return;
    out.push(node.id);
    if (node.expanded) for (const child of node.children) walk(child);
  };
  walk(root);
  return out;
}

function allIds(root: TreeNode): string[] {
  const out: string[] = [];
  const walk = (node: TreeNode): void => {
    out.push(node.id);
    for (const child of node.children) walk(child);
  };
  walk(root);
  return out;
}

function snapshot(view: TreeView): unknown {
  const model = view.model;
  const expanded: string[] = [];
  const walk = (n: TreeNode): void => {
    if (n.expanded) expanded.push(n.id);
    for (const c of n.children) walk(c);
  };
  walk(model.root);
  return {
    version: model.version,
    focus: view.focus(),
    selection: view.selection().sort(),
    anchor: view.anchor(),
    typeBuffer: view.typeAheadBuffer,
    visible: model.visibleIds(),
    expanded,
  };
}

function checkInvariants(view: TreeView, lastVersion: number): void {
  const model = view.model;
  assert.ok(model.version >= lastVersion, "version is monotonic");
  // focus: null or enabled + visible
  const focus = view.focus();
  if (focus !== null) {
    const node = model.get(focus)!;
    assert.ok(!node.disabled, `focus ${focus} must be enabled`);
    assert.ok(model.isVisible(node), `focus ${focus} must be visible`);
  }
  // selection: no disabled, no unknown ids
  for (const id of view.selection()) {
    const node = model.get(id);
    assert.ok(node, `selected ${id} must exist`);
    assert.ok(!node!.disabled, `selected ${id} must be enabled`);
  }
  // anchor: null or enabled + visible
  const anchor = view.anchor();
  if (anchor !== null) {
    const node = model.get(anchor)!;
    assert.ok(!node.disabled, `anchor ${anchor} must be enabled`);
    assert.ok(model.isVisible(node), `anchor ${anchor} must be visible`);
  }
  // incremental visible sequence equals the brute-force oracle
  assert.deepEqual(model.visibleIds(), bruteForceVisible(model.root, model));
  // indexOf/at inverse on a sample
  const total = model.totalVisible;
  for (const i of [0, 1, total >> 1, total - 1]) {
    if (i >= 0 && i < total) assert.equal(model.indexOf(model.at(i)!), i);
  }
}

const TYPE_CHARS = ["a", "b", "c", "d", "g", "s", "r", "l"];
const FILTERS: (string | null)[] = [null, "a", "i", "md", "sh"];

async function runExploration(seed: number, steps: number, record: Ev[] | null): Promise<{ view: TreeView; log: Ev[] }> {
  const rand = mulberry32(seed);
  const loader = new DeferredLoader(lazyChildrenFixture());
  const model = new TreeModel(treeFixture(), loader.load);
  const view = new TreeView(model);
  const log: Ev[] = [];
  let insertCounter = 0;
  let lastVersion = model.version;

  const pick = <T>(items: T[]): T => items[Math.floor(rand() * items.length)];

  for (let step = 0; step < steps; step++) {
    let ev: Ev;
    if (record) {
      ev = record[step];
    } else {
      const visible = model.visibleIds();
      const ids = allIds(model.root);
      const kind = Math.floor(rand() * 15);
      switch (kind) {
        case 0: ev = { k: "toggle", id: pick(visible) }; break;
        case 1: ev = { k: "collapse", id: pick(visible) }; break;
        case 2: ev = { k: "nav", dir: pick(["up", "down", "home", "end"] as const) }; break;
        case 3: ev = { k: "type", ch: pick(TYPE_CHARS) }; break;
        case 4: ev = { k: "clearType" }; break;
        case 5: ev = { k: "select", id: pick(visible) }; break;
        case 6: ev = { k: "ctrl", id: pick(visible) }; break;
        case 7: ev = { k: "shift", id: pick(visible) }; break;
        case 8: ev = { k: "insert", parent: pick(ids), id: `gen-${insertCounter++}` }; break;
        case 9: ev = { k: "remove", id: pick(ids.filter((i) => i !== "root")) }; break;
        case 10: ev = { k: "move", id: pick(ids.filter((i) => i !== "root")), parent: pick(ids) }; break;
        case 11: ev = { k: "filter", q: pick(FILTERS) }; break;
        case 12: ev = { k: "disable", id: pick(ids), value: rand() < 0.5 }; break;
        case 13: {
          const loading = ids.filter((i) => model.get(i)!.loading);
          ev = loading.length ? { k: "cancelLoad", id: pick(loading) } : { k: "nav", dir: "down" };
          break;
        }
        default: {
          const pending = ids.filter((i) => loader.pendingCount(i) > 0);
          ev = pending.length ? { k: "resolve", id: pick(pending) } : { k: "nav", dir: "up" };
          break;
        }
      }
      log.push(ev);
    }

    // Apply the event; unknown-target errors are part of the model contract.
    try {
      switch (ev.k) {
        case "toggle": view.toggle(ev.id); break;
        case "collapse": {
          const n = model.get(ev.id);
          if (n) view.collapseNode(n);
          break;
        }
        case "nav":
          if (ev.dir === "up") view.moveUp();
          else if (ev.dir === "down") view.moveDown();
          else if (ev.dir === "home") view.home();
          else view.end();
          break;
        case "type": view.type(ev.ch); break;
        case "clearType": view.clearTypeAhead(); break;
        case "select": view.select(ev.id); break;
        case "ctrl": view.toggleSelect(ev.id); break;
        case "shift": view.selectRange(ev.id); break;
        case "insert": model.insert(ev.parent, { id: ev.id, label: `gen ${ev.id}` }); break;
        case "remove": if (model.has(ev.id)) view.remove(ev.id); break;
        case "move": if (model.has(ev.id) && model.has(ev.parent)) model.move(ev.id, ev.parent); break;
        case "filter": model.setFilter(ev.q === null ? null : (label) => label.includes(ev.q!)); break;
        case "disable": if (model.has(ev.id)) model.setDisabled(ev.id, ev.value); break;
        case "cancelLoad": if (model.has(ev.id)) model.cancelLoad(ev.id); break;
        case "resolve": if (loader.pendingCount(ev.id) > 0) loader.resolveNext(ev.id); break;
      }
    } catch (err) {
      // move-into-own-subtree is rejected by the model; that is expected.
      assert.ok(err instanceof Error && err.message.includes("own subtree"), `unexpected error: ${err}`);
    }
    await flush();
    checkInvariants(view, lastVersion);
    lastVersion = model.version;
  }
  return { view, log };
}

test("finite event exploration keeps every invariant", async () => {
  for (const seed of [1, 7, 42, 1337]) {
    await runExploration(seed, 250, null);
  }
});

test("replay consistency: same seed regenerates, same log replays", async () => {
  const first = await runExploration(2026, 300, null);
  const second = await runExploration(2026, 300, null);
  assert.deepEqual(second.log, first.log, "same seed generates the same event log");
  assert.deepEqual(snapshot(second.view), snapshot(first.view), "same seed reaches the same state");

  const replayed = await runExploration(0, 300, first.log);
  assert.deepEqual(snapshot(replayed.view), snapshot(first.view), "replaying the log reproduces the state");
});
