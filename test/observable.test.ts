import { test } from "node:test";
import assert from "node:assert/strict";
import { Observable, ObserverNotificationError } from "../src/observable.ts";

test("notifies listeners with value and previous", () => {
  const log: string[] = [];
  const obs = new Observable(0);
  obs.subscribe((value, previous) => log.push(`a:${previous}->${value}`));
  obs.set(1);
  assert.deepEqual(log, ["a:0->1"]);
  assert.equal(obs.get(), 1);
});

test("set with an equal value does not notify", () => {
  const log: string[] = [];
  const obs = new Observable(1);
  obs.subscribe(() => log.push("hit"));
  obs.set(1);
  assert.deepEqual(log, []);
});

test("runs listeners by priority, then registration order", () => {
  const log: string[] = [];
  const obs = new Observable(0);
  obs.subscribe(() => log.push("low-1"), 10);
  obs.subscribe(() => log.push("high-1"), -10);
  obs.subscribe(() => log.push("mid"), 0);
  obs.subscribe(() => log.push("high-2"), -10);
  obs.subscribe(() => log.push("low-2"), 10);
  obs.set(1);
  assert.deepEqual(log, ["high-1", "high-2", "mid", "low-1", "low-2"]);
});

test("round uses a fixed snapshot: listeners added mid-round join next round", () => {
  const log: string[] = [];
  const obs = new Observable(0);
  let added = false;
  obs.subscribe((value) => {
    log.push(`adder:${value}`);
    if (!added) {
      added = true;
      obs.subscribe((v) => log.push(`late:${v}`));
      obs.set(2); // force a second round so the late listener runs
    }
  });
  obs.set(1);
  assert.deepEqual(log, ["adder:1", "adder:2", "late:2"]);
});

test("unsubscribe immediately blocks a not-yet-executed listener", () => {
  const log: string[] = [];
  const obs = new Observable(0);
  const unsubB = obs.subscribe(() => log.push("b"));
  obs.subscribe(() => {
    log.push("a");
    unsubB();
  }, -1); // runs before b
  obs.set(1);
  assert.deepEqual(log, ["a"]);
  assert.equal(obs.listenerCount(), 1);
});

test("unsubscribed listener is skipped in every later round", () => {
  const log: string[] = [];
  const obs = new Observable(0);
  const unsub = obs.subscribe((value) => log.push(`x:${value}`));
  obs.set(1);
  unsub();
  obs.set(2);
  assert.deepEqual(log, ["x:1"]);
});

test("nested updates are merged into a single next round", () => {
  const log: string[] = [];
  const obs = new Observable(0);
  obs.subscribe((value, previous) => {
    log.push(`seen:${previous}->${value}`);
    if (value === 1) {
      obs.set(2);
      obs.set(3); // overwrites 2 before any listener observes it
    }
  });
  obs.set(1);
  assert.deepEqual(log, ["seen:0->1", "seen:1->3"]);
  assert.equal(obs.get(), 3);
});

test("value returning to the round's original value cancels further rounds", () => {
  const log: string[] = [];
  const obs = new Observable(0);
  obs.subscribe((value) => {
    log.push(`seen:${value}`);
    if (value === 1) obs.set(0); // restore original before round ends
  });
  obs.set(1);
  assert.deepEqual(log, ["seen:1"]);
  assert.equal(obs.get(), 0);
});

test("re-entrant chain converges and reports each round's previous", () => {
  const log: string[] = [];
  const obs = new Observable(0);
  obs.subscribe((value, previous) => {
    log.push(`${previous}->${value}`);
    if (value < 3) obs.set(value + 1);
  });
  obs.set(1);
  assert.deepEqual(log, ["0->1", "1->2", "2->3"]);
});

test("exceptions are collected, remaining listeners run, set rethrows", () => {
  const log: string[] = [];
  const obs = new Observable(0);
  const boom1 = new Error("boom-1");
  const boom2 = new Error("boom-2");
  obs.subscribe(() => { throw boom1; }, -1);
  obs.subscribe(() => log.push("middle"));
  obs.subscribe(() => { throw boom2; }, 1);
  obs.subscribe(() => log.push("tail"), 2);

  let caught: unknown;
  try {
    obs.set(1);
  } catch (error) {
    caught = error;
  }
  assert.ok(caught instanceof ObserverNotificationError);
  assert.deepEqual((caught as ObserverNotificationError).errors, [boom1, boom2]);
  assert.deepEqual(log, ["middle", "tail"]);
  assert.equal(obs.get(), 1);
});

test("exceptions in later rounds are collected too", () => {
  const log: string[] = [];
  const obs = new Observable(0);
  const boom = new Error("round-2");
  obs.subscribe((value) => {
    log.push(`a:${value}`);
    if (value === 1) obs.set(2);
  }, -1);
  obs.subscribe((value) => {
    if (value === 2) throw boom;
  });
  let caught: unknown;
  try {
    obs.set(1);
  } catch (error) {
    caught = error;
  }
  assert.ok(caught instanceof ObserverNotificationError);
  assert.deepEqual((caught as ObserverNotificationError).errors, [boom]);
  assert.deepEqual(log, ["a:1", "a:2"]);
});
