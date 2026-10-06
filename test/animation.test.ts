import test from "node:test";
import assert from "node:assert/strict";
import { Animation } from "../src/index.ts";
import type { FinishEvent } from "../src/index.ts";

test("linear ramp over one iteration, end value kept after finish", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100 });
  a.play(0);
  assert.equal(a.sample(0), 0);
  assert.equal(a.sample(50), 50);
  assert.equal(a.sample(99), 99);
  assert.equal(a.sample(100), 100);
  assert.equal(a.currentState, "finished");
  assert.equal(a.sample(150), 100);
});

test("delay holds the start value before the active phase", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100, delay: 50 });
  a.play(0);
  assert.equal(a.sample(25), 0);
  assert.equal(a.sample(50), 0); // delay boundary belongs to period 1 start
  assert.equal(a.sample(75), 25);
  assert.equal(a.sample(100), 50);
  assert.equal(a.sample(150), 100);
});

test("endpoint ownership: boundaries belong to the new period, final end keeps end value", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100, iterations: 3 });
  a.play(0);
  assert.equal(a.sample(99), 99);
  assert.equal(a.sample(100), 0); // start of period 2, not end of period 1
  assert.equal(a.sample(199), 99);
  assert.equal(a.sample(200), 0); // start of period 3
  assert.equal(a.sample(299), 99);
  assert.equal(a.sample(300), 100); // end of last period keeps the end value
  assert.equal(a.sample(400), 100); // filled forward
  assert.equal(a.currentState, "finished");
});

test("alternate (yoyo): odd periods play in reverse", () => {
  const a = new Animation({
    from: 0,
    to: 100,
    duration: 100,
    iterations: 2,
    direction: "alternate",
  });
  a.play(0);
  assert.equal(a.sample(50), 50);
  assert.equal(a.sample(100), 100); // start of reversed period 2
  assert.equal(a.sample(150), 50);
  assert.equal(a.sample(200), 0); // final end of reversed last period
  assert.equal(a.sample(250), 0);
});

test("alternate with odd iterations ends on the forward end value", () => {
  const a = new Animation({
    from: 0,
    to: 100,
    duration: 100,
    iterations: 3,
    direction: "alternate",
  });
  a.play(0);
  assert.equal(a.sample(250), 50);
  assert.equal(a.sample(300), 100);
});

test("fractional iterations end mid-period", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100, iterations: 1.5 });
  a.play(0);
  assert.equal(a.sample(149), 49);
  assert.equal(a.sample(150), 50); // halfway through period 2
  assert.equal(a.currentState, "finished");
});

test("zero duration is a step at the delay point", () => {
  const a = new Animation({ from: 0, to: 50, duration: 0, delay: 100 });
  a.play(0);
  assert.equal(a.sample(0), 0);
  assert.equal(a.sample(99), 0);
  assert.equal(a.sample(100), 50); // end of last period: final value
  assert.equal(a.sample(500), 50);
  assert.equal(a.currentState, "finished");
});

test("zero duration fires finish exactly once", () => {
  const a = new Animation({ from: 0, to: 50, duration: 0 });
  const events: FinishEvent[] = [];
  a.on("finish", (e) => events.push(e));
  a.play(0);
  a.sample(0);
  a.sample(10);
  a.sample(20);
  assert.equal(events.length, 1);
  assert.deepEqual(events[0], { globalTime: 0, localTime: 0 });
});

test("zero duration with alternate and even iterations ends on the start value", () => {
  const a = new Animation({
    from: 0,
    to: 50,
    duration: 0,
    iterations: 2,
    direction: "alternate",
  });
  a.play(0);
  assert.equal(a.sample(0), 0); // last period is reversed: final value is `from`
});

test("zero duration with negative rate steps backwards through the delay", () => {
  const a = new Animation({
    from: 0,
    to: 50,
    duration: 0,
    delay: 100,
    playbackRate: -1,
  });
  a.play(0);
  assert.equal(a.sample(0), 50); // starts at endTime = delay -> final value
  assert.equal(a.sample(50), 0); // local 50 < delay -> start value
  assert.equal(a.sample(100), 0); // local 0 -> finishes
  assert.equal(a.currentState, "finished");
});

test("negative playback rate plays the timeline in reverse", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100, playbackRate: -1 });
  const events: FinishEvent[] = [];
  a.on("finish", (e) => events.push(e));
  a.play(0);
  assert.equal(a.sample(0), 100); // fresh negative-rate play starts at the end
  assert.equal(a.sample(25), 75);
  assert.equal(a.sample(99), 1);
  assert.equal(a.sample(100), 0); // local time 0 -> finish
  assert.equal(a.currentState, "finished");
  assert.equal(events.length, 1);
  assert.deepEqual(events[0], { globalTime: 100, localTime: 0 });
});

test("negative rate with repeats and yoyo starts from the yoyo end value", () => {
  const a = new Animation({
    from: 0,
    to: 100,
    duration: 100,
    iterations: 2,
    direction: "alternate",
    playbackRate: -1,
  });
  a.play(0);
  assert.equal(a.sample(0), 0); // end of reversed last period is `from`
  assert.equal(a.sample(50), 50); // moving backwards through period 2
  assert.equal(a.sample(100), 100); // boundary: start of period 2 == end of period 1
  assert.equal(a.sample(150), 50);
  assert.equal(a.sample(200), 0);
  assert.equal(a.currentState, "finished");
});

test("playback rate changes keep local time continuous", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100 });
  a.play(0);
  a.setPlaybackRate(2, 50); // local time 50
  assert.equal(a.sample(60), 70); // 50 + 10 * 2
  a.setPlaybackRate(-1, 60); // local time 70
  assert.equal(a.sample(70), 60);
  assert.equal(a.sample(130), 0); // local time 0 -> finish
  assert.equal(a.currentState, "finished");
});

test("pause and resume never jump", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100 });
  a.play(0);
  assert.equal(a.sample(40), 40);
  a.pause(40);
  assert.equal(a.sample(41), 40);
  assert.equal(a.sample(1000), 40); // frozen while paused
  a.play(1000);
  assert.equal(a.sample(1000), 40); // resume instant: identical value
  assert.equal(a.sample(1010), 50);
  assert.equal(a.sample(1060), 100);
  assert.equal(a.currentState, "finished");
});

test("same-timestamp pause+play collapses to one segment (piecewise map)", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100 });
  a.play(0);
  a.pause(50);
  a.play(50);
  assert.deepEqual(a.getSegments(), [
    { startGlobal: 0, startLocal: 0, rate: 1 },
    { startGlobal: 50, startLocal: 50, rate: 1 },
  ]);
  assert.equal(a.localTimeAt(80), 80);
  assert.equal(a.sample(50), 50);
  assert.equal(a.sample(80), 80);
});

test("same-timestamp rate changes: last one wins, no zero-length segments", () => {
  const a = new Animation({ from: 0, to: 100, duration: 1000 });
  a.play(0);
  a.setPlaybackRate(2, 50);
  a.setPlaybackRate(3, 50);
  const segments = a.getSegments();
  assert.equal(segments.length, 2);
  assert.deepEqual(segments[1], { startGlobal: 50, startLocal: 50, rate: 3 });
  assert.equal(a.localTimeAt(60), 80); // 50 + 10 * 3
});

test("same-timestamp pause, rate change and resume stay continuous", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100 });
  a.play(0);
  a.pause(60);
  a.setPlaybackRate(2, 60); // paused: stored, applied on resume
  a.play(60);
  assert.deepEqual(a.getSegments(), [
    { startGlobal: 0, startLocal: 0, rate: 1 },
    { startGlobal: 60, startLocal: 60, rate: 2 },
  ]);
  assert.equal(a.sample(60), 60); // no jump at the busy timestamp
  assert.equal(a.sample(70), 80);
});

test("operations at the play timestamp itself collapse too", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100 });
  a.play(0);
  a.pause(0);
  a.play(0);
  assert.deepEqual(a.getSegments(), [{ startGlobal: 0, startLocal: 0, rate: 1 }]);
  assert.equal(a.sample(10), 10);
});

test("operation timestamps must not go backward", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100 });
  a.play(0);
  a.pause(60);
  assert.throws(() => a.play(30), RangeError);
  assert.throws(() => a.setPlaybackRate(2, 30), RangeError);
});

test("restart at the exact finish timestamp works", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100 });
  a.play(0);
  assert.equal(a.sample(100), 100);
  a.play(100);
  assert.equal(a.currentState, "running");
  assert.equal(a.sample(150), 50);
});

test("cancel emits no finish event and resets to idle", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100 });
  let finishes = 0;
  a.on("finish", () => finishes++);
  a.play(0);
  a.sample(90);
  a.cancel();
  assert.equal(finishes, 0);
  assert.equal(a.currentState, "idle");
  assert.equal(a.sample(200), undefined);
  assert.equal(a.localTimeAt(200), undefined);
  a.play(0); // restart from scratch
  assert.equal(a.sample(50), 50);
  assert.equal(a.sample(100), 100);
  assert.equal(finishes, 1);
});

test("cancel while paused emits nothing", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100 });
  let finishes = 0;
  a.on("finish", () => finishes++);
  a.play(0);
  a.pause(50);
  a.cancel();
  assert.equal(finishes, 0);
  assert.equal(a.currentState, "idle");
});

test("finish fires exactly once and not while paused", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100 });
  const events: FinishEvent[] = [];
  a.on("finish", (e) => events.push(e));
  a.play(0);
  a.pause(90);
  a.sample(500); // paused: no finish even past the end
  assert.equal(events.length, 0);
  a.play(90); // same-timestamp resume
  a.sample(100);
  a.sample(200);
  assert.equal(events.length, 1);
  assert.deepEqual(events[0], { globalTime: 100, localTime: 100 });
});

test("easing is applied to the directed phase", () => {
  const a = new Animation({
    from: 0,
    to: 100,
    duration: 100,
    easing: (t) => t * t,
  });
  a.play(0);
  assert.equal(a.sample(50), 25);
  assert.equal(a.sample(100), 100);
});

test("idle animation samples to undefined", () => {
  const a = new Animation({ from: 0, to: 100, duration: 100 });
  assert.equal(a.sample(0), undefined);
  assert.equal(a.localTimeAt(0), undefined);
});

test("invalid options are rejected", () => {
  assert.throws(() => new Animation({ from: 0, to: 1, duration: -1 }), RangeError);
  assert.throws(
    () => new Animation({ from: 0, to: 1, duration: 1, delay: -1 }),
    RangeError,
  );
  assert.throws(
    () => new Animation({ from: 0, to: 1, duration: 1, iterations: 0 }),
    RangeError,
  );
  assert.throws(
    () => new Animation({ from: 0, to: 1, duration: Number.NaN }),
    TypeError,
  );
});
