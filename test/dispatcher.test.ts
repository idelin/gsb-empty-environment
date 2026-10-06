import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ShortcutDispatcher,
  type Binding,
  type ShortcutConfig,
} from "../src/dispatcher.ts";

/** Recorded command emission. */
interface Emitted {
  command: string;
  time: number;
}

/**
 * Explicit-clock event script. Every event carries its logical timestamp;
 * replaying a script is fully deterministic, including events that share
 * the same timestamp (they apply in script order).
 */
type Event =
  | { type: "key"; key: string; time: number }
  | { type: "tick"; time: number }
  | { type: "focus"; time: number }
  | { type: "stack"; stack: string[]; time: number }
  | { type: "config"; config: ShortcutConfig; time: number }
  | { type: "pause" }
  | { type: "resume" };

function key(key: string, time: number): Event {
  return { type: "key", key, time };
}
function tick(time: number): Event {
  return { type: "tick", time };
}
function focus(time: number): Event {
  return { type: "focus", time };
}
function stack(stack: string[], time: number): Event {
  return { type: "stack", stack, time };
}
function config(config: ShortcutConfig, time: number): Event {
  return { type: "config", config, time };
}
const pause: Event = { type: "pause" };
const resume: Event = { type: "resume" };

function binding(seq: string, command: string): Binding {
  return { sequence: seq.split(" "), command };
}

function makeDispatcher(
  cfg: ShortcutConfig,
  contextStack: string[] = ["global"],
): { dispatcher: ShortcutDispatcher; emitted: Emitted[] } {
  const emitted: Emitted[] = [];
  const dispatcher = new ShortcutDispatcher(cfg, contextStack, (command, time) =>
    emitted.push({ command, time }),
  );
  return { dispatcher, emitted };
}

function replay(dispatcher: ShortcutDispatcher, events: Event[]): void {
  for (const event of events) {
    switch (event.type) {
      case "key":
        dispatcher.keyDown(event.key, event.time);
        break;
      case "tick":
        dispatcher.tick(event.time);
        break;
      case "focus":
        dispatcher.focusChanged(event.time);
        break;
      case "stack":
        dispatcher.setContextStack(event.stack, event.time);
        break;
      case "config":
        dispatcher.updateConfig(event.config, event.time);
        break;
      case "pause":
        dispatcher.pause();
        break;
      case "resume":
        dispatcher.resume();
        break;
    }
  }
}

const TIMEOUT = 500;

/** Nested prefixes: "a" < "a b" < "a b c". */
const nestedConfig: ShortcutConfig = {
  timeoutMs: TIMEOUT,
  contexts: {
    global: [
      binding("a", "A"),
      binding("a b", "AB"),
      binding("a b c", "ABC"),
    ],
  },
};

test("nested prefixes: full sequence wins when typed within the deadline", () => {
  const { dispatcher, emitted } = makeDispatcher(nestedConfig);
  replay(dispatcher, [
    key("a", 0),
    key("b", 100),
    key("c", 200),
  ]);
  assert.deepEqual(emitted, [{ command: "ABC", time: 200 }]);
  assert.deepEqual(dispatcher.getState().buffer, []);
});

test("nested prefixes: intermediate short command fires at its own deadline", () => {
  const { dispatcher, emitted } = makeDispatcher(nestedConfig);
  replay(dispatcher, [
    key("a", 0), // deadline 500, pending "A"
    key("b", 100), // re-resolved: deadline 600, pending "AB"
    tick(600), // exactly at the "AB" deadline
  ]);
  assert.deepEqual(emitted, [{ command: "AB", time: 600 }]);
});

test("nested prefixes: tick exactly at the deadline fires, tick before does not", () => {
  const { dispatcher, emitted } = makeDispatcher(nestedConfig);
  replay(dispatcher, [
    key("a", 0), // deadline 500
    tick(499),
  ]);
  assert.deepEqual(emitted, []);
  replay(dispatcher, [tick(500)]);
  assert.deepEqual(emitted, [{ command: "A", time: 500 }]);
});

test("exact expiry: a key arriving exactly at the deadline fires the short command first", () => {
  const cfg: ShortcutConfig = {
    timeoutMs: TIMEOUT,
    contexts: {
      global: [binding("a", "A"), binding("a b", "AB"), binding("b", "B")],
    },
  };
  const { dispatcher, emitted } = makeDispatcher(cfg);
  replay(dispatcher, [
    key("a", 0), // deadline 500, pending "A"
    key("b", 500), // exactly at the deadline: "A" fires, then "b" re-matches fresh
  ]);
  assert.deepEqual(emitted, [
    { command: "A", time: 500 },
    { command: "B", time: 500 },
  ]);
});

test("mismatch: pending short command fires, then the current key is re-matched", () => {
  const cfg: ShortcutConfig = {
    timeoutMs: TIMEOUT,
    contexts: {
      global: [
        binding("a", "A"),
        binding("a b", "AB"),
        binding("x", "X"),
        binding("x y", "XY"),
      ],
    },
  };
  const { dispatcher, emitted } = makeDispatcher(cfg);
  replay(dispatcher, [
    key("a", 0), // pending "A"
    key("x", 100), // "a x" matches nothing: fire "A", re-match "x" -> pending "X"
    key("y", 200), // completes "x y"
  ]);
  assert.deepEqual(emitted, [
    { command: "A", time: 100 },
    { command: "XY", time: 200 },
  ]);
});

test("mismatch: pure prefix with no short command emits nothing on mismatch", () => {
  const cfg: ShortcutConfig = {
    timeoutMs: TIMEOUT,
    contexts: { global: [binding("a b", "AB")] },
  };
  const { dispatcher, emitted } = makeDispatcher(cfg);
  replay(dispatcher, [
    key("a", 0), // pure prefix, pending with null command
    key("z", 100), // "a z" matches nothing, "a" has no exact command
    tick(1000),
  ]);
  assert.deepEqual(emitted, []);
  assert.deepEqual(dispatcher.getState().buffer, []);
});

test("focus change cancels the pending sequence without firing", () => {
  const { dispatcher, emitted } = makeDispatcher(nestedConfig);
  replay(dispatcher, [
    key("a", 0), // pending "A", deadline 500
    focus(100), // cancel
    tick(1000), // nothing fires
    key("b", 1100), // "b" alone matches nothing
  ]);
  assert.deepEqual(emitted, []);
  assert.deepEqual(dispatcher.getState().buffer, []);
});

test("context stack: overlay exact match shadows global sequences immediately", () => {
  const cfg: ShortcutConfig = {
    timeoutMs: TIMEOUT,
    contexts: {
      global: [binding("g", "G"), binding("g d", "GD")],
      modal: [binding("g", "MODAL_G")],
    },
  };
  const { dispatcher, emitted } = makeDispatcher(cfg, ["global", "modal"]);
  replay(dispatcher, [key("g", 0)]);
  // The modal's exact "g" shadows the global "g"/"g d" ambiguity entirely.
  assert.deepEqual(emitted, [{ command: "MODAL_G", time: 0 }]);
});

test("context stack: overlay longer sequence shadows the global short command", () => {
  const cfg: ShortcutConfig = {
    timeoutMs: TIMEOUT,
    contexts: {
      global: [binding("g", "G"), binding("g d", "GD")],
      modal: [binding("g d", "MODAL_GD")],
    },
  };
  const { dispatcher, emitted } = makeDispatcher(cfg, ["global", "modal"]);
  replay(dispatcher, [
    key("g", 0), // modal has only a longer candidate: pending with null command
    tick(1000), // global "G" is shadowed, nothing fires
  ]);
  assert.deepEqual(emitted, []);
  replay(dispatcher, [
    key("g", 2000),
    key("d", 2100),
  ]);
  assert.deepEqual(emitted, [{ command: "MODAL_GD", time: 2100 }]);
});

test("context change during prefix wait re-adjudicates typed keys without re-triggering", () => {
  const cfg: ShortcutConfig = {
    timeoutMs: TIMEOUT,
    contexts: {
      global: [binding("a", "A"), binding("a b", "AB")],
      modal: [binding("a b", "MODAL_AB")],
    },
  };
  const { dispatcher, emitted } = makeDispatcher(cfg);
  replay(dispatcher, [
    key("a", 0), // pending "A", deadline 500
    stack(["global", "modal"], 100), // re-adjudicate: modal shadows, command -> null
    key("b", 200), // completes "a b" in the modal context
    stack(["global"], 300), // buffer already consumed; nothing happens
    tick(1000), // no late timeout may fire
  ]);
  // "A" must never fire: it was shadowed before its deadline, and
  // re-adjudication must not trigger commands.
  assert.deepEqual(emitted, [{ command: "MODAL_AB", time: 200 }]);
});

test("context change that voids the buffered keys drops them silently", () => {
  const cfg: ShortcutConfig = {
    timeoutMs: TIMEOUT,
    contexts: {
      global: [binding("a", "A"), binding("a b", "AB")],
      modal: [binding("q", "Q")],
    },
  };
  const { dispatcher, emitted } = makeDispatcher(cfg);
  replay(dispatcher, [
    key("a", 0), // pending "A"
    stack(["modal"], 100), // global removed: "a" matches nothing anymore
    tick(1000), // the dropped pending state must not fire
    key("b", 1100), // "b" alone matches nothing in the modal context
  ]);
  assert.deepEqual(emitted, []);
  assert.deepEqual(dispatcher.getState().buffer, []);
});

test("atomic config hot-reload re-adjudicates pending keys and bumps the generation", () => {
  const cfgV1: ShortcutConfig = {
    timeoutMs: TIMEOUT,
    contexts: { global: [binding("a", "A1"), binding("a b", "AB1")] },
  };
  const cfgV2: ShortcutConfig = {
    timeoutMs: TIMEOUT,
    contexts: { global: [binding("a", "A2"), binding("a b", "AB2")] },
  };
  const { dispatcher, emitted } = makeDispatcher(cfgV1);
  replay(dispatcher, [
    key("a", 0), // pending "A1", deadline 500, generation 0
    config(cfgV2, 100), // atomic swap: re-adjudicated to "A2", deadline kept
  ]);
  const state = dispatcher.getState();
  assert.equal(state.generation, 1);
  assert.deepEqual(state.pending, { command: "A2", deadline: 500, generation: 1 });
  replay(dispatcher, [tick(500)]);
  assert.deepEqual(emitted, [{ command: "A2", time: 500 }]);
});

test("config hot-reload that removes the sequence drops the pending keys silently", () => {
  const cfgV1: ShortcutConfig = {
    timeoutMs: TIMEOUT,
    contexts: { global: [binding("a", "A"), binding("a b", "AB")] },
  };
  const cfgV2: ShortcutConfig = {
    timeoutMs: TIMEOUT,
    contexts: { global: [binding("z", "Z")] },
  };
  const { dispatcher, emitted } = makeDispatcher(cfgV1);
  replay(dispatcher, [
    key("a", 0),
    config(cfgV2, 100), // "a" no longer bound: pending dropped, no emission
    tick(1000),
    key("z", 1100), // new config is live
  ]);
  assert.deepEqual(emitted, [{ command: "Z", time: 1100 }]);
});

test("pause/resume preserves the pending sequence and its generation", () => {
  const { dispatcher, emitted } = makeDispatcher(nestedConfig);
  replay(dispatcher, [
    key("a", 0), // pending "A", deadline 500, generation 0
    pause,
    tick(600), // dropped: clock events are ignored while paused
    key("b", 700), // dropped: key events are ignored while paused
    resume,
  ]);
  const state = dispatcher.getState();
  assert.equal(state.generation, 0);
  assert.deepEqual(state.buffer, ["a"]);
  assert.deepEqual(state.pending, { command: "A", deadline: 500, generation: 0 });
  replay(dispatcher, [tick(800)]);
  assert.deepEqual(emitted, [{ command: "A", time: 800 }]);
});

test("same-timestamp replay: key, config swap, and focus at one logical instant", () => {
  const cfgV1: ShortcutConfig = {
    timeoutMs: TIMEOUT,
    contexts: { global: [binding("g", "G1"), binding("g d", "GD1")] },
  };
  const cfgV2: ShortcutConfig = {
    timeoutMs: TIMEOUT,
    contexts: { global: [binding("g", "G2")] },
  };
  // Key and config swap at the same timestamp: the pending command is
  // re-adjudicated under the new config but keeps its original deadline.
  const first = makeDispatcher(cfgV1);
  replay(first.dispatcher, [
    key("g", 1000),
    config(cfgV2, 1000),
    tick(1500),
  ]);
  assert.deepEqual(first.emitted, [{ command: "G2", time: 1500 }]);

  // Key and focus change at the same timestamp: focus cancels, nothing fires.
  const second = makeDispatcher(cfgV1);
  replay(second.dispatcher, [
    key("g", 1000),
    focus(1000),
    tick(1500),
  ]);
  assert.deepEqual(second.emitted, []);

  // Config swap and key at the same timestamp, in the opposite order.
  const third = makeDispatcher(cfgV1);
  replay(third.dispatcher, [
    config(cfgV2, 1000),
    key("g", 1000), // "g" is unambiguous under cfgV2: fires immediately
  ]);
  assert.deepEqual(third.emitted, [{ command: "G2", time: 1000 }]);
});

test("same-timestamp replay: keys typed at one logical instant still nest", () => {
  const { dispatcher, emitted } = makeDispatcher(nestedConfig);
  replay(dispatcher, [
    key("a", 1000),
    key("b", 1000),
    key("c", 1000),
  ]);
  assert.deepEqual(emitted, [{ command: "ABC", time: 1000 }]);
});
