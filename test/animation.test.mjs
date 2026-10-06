import { Animation } from '../dist/animation.js';
import { test, eq, near } from './harness.mjs';

const recorder = () => {
  const events = [];
  return { events, onfinish: (t) => events.push(t) };
};

test('idle before play; sample returns undefined', () => {
  const a = new Animation({ from: 0, to: 100, duration: 1000 });
  eq(a.state, 'idle');
  eq(a.sample(0), undefined);
  eq(a.sample(500), undefined);
});

test('delay holds the start value, then interpolates linearly', () => {
  const { events, onfinish } = recorder();
  const a = new Animation({ from: 0, to: 100, duration: 1000, delay: 200, onfinish });
  a.play(0);
  eq(a.sample(0), 0);
  eq(a.sample(199), 0, 'before delay ends');
  eq(a.sample(200), 0, 'active phase starts at 0');
  eq(a.sample(700), 50);
  eq(a.sample(1200), 100, 'end keeps the final value');
  eq(a.state, 'finished');
  eq(a.sample(1500), 100, 'holds final value after finishing');
  eq(events.length, 1, 'onfinish fired once');
  eq(events[0], 1200, 'finish time');
  a.sample(2000);
  eq(events.length, 1, 'onfinish not refired');
});

test('endpoint ownership: interior boundary belongs to the new iteration start', () => {
  const a = new Animation({ from: 0, to: 10, duration: 100, iterations: 3 });
  a.play(0);
  near(a.sample(99.9), 9.99, 'just before iteration 1');
  eq(a.sample(100), 0, 't=100 is the start of iteration 1');
  near(a.sample(199.9), 9.99, 'just before iteration 2');
  eq(a.sample(200), 0, 't=200 is the start of iteration 2');
  near(a.sample(299.9), 9.99, 'just before the end');
  eq(a.sample(300), 10, 'last iteration end keeps the final value');
  eq(a.sample(400), 10, 'after phase holds the final value');
});

test('yoyo (alternate) reverses every other iteration', () => {
  const a = new Animation({ from: 0, to: 100, duration: 100, iterations: 2, direction: 'alternate' });
  a.play(0);
  eq(a.sample(50), 50);
  eq(a.sample(100), 100, 'iteration 1 starts directed at 1');
  eq(a.sample(150), 50, 'second iteration plays backwards');
  eq(a.sample(200), 0, 'final value is the end of the last (odd) iteration');
  eq(a.state, 'finished');
});

test('alternate-reverse starts at the end value', () => {
  const a = new Animation({ from: 0, to: 100, duration: 100, direction: 'alternate-reverse' });
  a.play(0);
  eq(a.sample(0), 100);
  eq(a.sample(50), 50);
  eq(a.sample(100), 0);
});

test('reverse plays from `to` to `from`', () => {
  const a = new Animation({ from: 0, to: 100, duration: 100, direction: 'reverse' });
  a.play(0);
  eq(a.sample(0), 100);
  eq(a.sample(25), 75);
  eq(a.sample(100), 0, 'final value of a reverse animation is `from`');
});

test('negative playback rate runs backwards and finishes at local 0', () => {
  const { events, onfinish } = recorder();
  const a = new Animation({ from: 0, to: 100, duration: 1000, playbackRate: -1, onfinish });
  a.play(0);
  a.pause(0);
  a.seek(1000, 0); // jump to the end, still paused
  a.resume(0); // all controls at the same timestamp
  eq(a.sample(0), 100);
  eq(a.sample(500), 50);
  eq(a.sample(1000), 0, 'reaches the start value');
  eq(a.state, 'finished');
  eq(events.length, 1);
  eq(events[0], 1000, 'finish time');
  eq(a.sample(1500), 0, 'holds the start value after finishing');
});

test('negative playback rate from local 0 finishes immediately', () => {
  const { events, onfinish } = recorder();
  const a = new Animation({ from: 0, to: 100, duration: 1000, playbackRate: -1, onfinish });
  a.play(0);
  eq(a.sample(0), 0);
  eq(a.state, 'finished');
  eq(events.length, 1);
  eq(events[0], 0);
});

test('pause/resume never jumps', () => {
  const { events, onfinish } = recorder();
  const a = new Animation({ from: 0, to: 100, duration: 1000, onfinish });
  a.play(0);
  eq(a.sample(400), 40);
  a.pause(400);
  eq(a.sample(400), 40, 'frozen at pause time');
  eq(a.sample(900), 40, 'stays frozen while paused');
  a.resume(1000);
  eq(a.sample(1000), 40, 'no jump on resume');
  eq(a.sample(1100), 50);
  eq(a.sample(1600), 100, 'finishes after 1000ms of running time');
  eq(events[0], 1600);
});

test('same-timestamp pause+resume is a no-op', () => {
  const { events, onfinish } = recorder();
  const a = new Animation({ from: 0, to: 100, duration: 1000, onfinish });
  a.play(0);
  a.pause(500);
  a.resume(500);
  eq(a.state, 'running');
  eq(a.sample(500), 50);
  eq(a.sample(1000), 100);
  eq(events[0], 1000);
});

test('same-timestamp pause+seek+resume applies the seek', () => {
  const { events, onfinish } = recorder();
  const a = new Animation({ from: 0, to: 100, duration: 1000, onfinish });
  a.play(0);
  a.pause(300);
  a.seek(500, 300);
  a.resume(300);
  eq(a.sample(300), 50, 'seek took effect at the same timestamp');
  eq(a.sample(800), 100, 'finishes 500ms later');
  eq(events[0], 800);
});

test('cancel emits no finish event and sampling stops', () => {
  const { events, onfinish } = recorder();
  const a = new Animation({ from: 0, to: 100, duration: 1000, onfinish });
  a.play(0);
  eq(a.sample(300), 30);
  a.cancel();
  eq(a.state, 'idle');
  eq(a.sample(500), undefined);
  eq(a.sample(5000), undefined, 'no finish after cancel');
  eq(events.length, 0, 'cancel never fires onfinish');
  a.play(0); // replaying after cancel works and can finish again
  eq(a.sample(1000), 100);
  eq(events.length, 1);
  eq(events[0], 1000);
});

test('zero-duration animation flips at the delay and finishes there', () => {
  const { events, onfinish } = recorder();
  const a = new Animation({
    from: 0,
    to: 100,
    duration: 0,
    delay: 100,
    iterations: 3,
    direction: 'alternate',
    onfinish,
  });
  a.play(0);
  eq(a.sample(50), 0, 'before phase holds the start value');
  eq(a.sample(99), 0);
  eq(a.sample(100), 100, 'final value of last (even) alternate iteration');
  eq(a.state, 'finished');
  eq(events.length, 1);
  eq(events[0], 100, 'finishes exactly at the delay');
  eq(a.sample(500), 100);
});

test('zero-duration with zero delay finishes at play time', () => {
  const a = new Animation({ from: 5, to: 9, duration: 0 });
  a.play(0);
  eq(a.sample(0), 9);
  eq(a.state, 'finished');
});

test('zero-duration final value honours direction and iteration parity', () => {
  const rev = new Animation({ from: 0, to: 100, duration: 0, direction: 'reverse' });
  rev.play(0);
  eq(rev.sample(0), 0, 'reverse ends at `from`');
  const yoyo = new Animation({ from: 0, to: 100, duration: 0, iterations: 2, direction: 'alternate' });
  yoyo.play(0);
  eq(yoyo.sample(0), 0, 'odd last iteration ends directed at 0');
});

test('fractional iterations end mid-iteration', () => {
  const a = new Animation({ from: 0, to: 100, duration: 100, iterations: 2.5 });
  a.play(0);
  eq(a.sample(200), 0, 'boundary owned by iteration 2 start');
  eq(a.sample(225), 25);
  eq(a.sample(250), 50, 'final value is half of the last iteration');
  eq(a.state, 'finished');
});

test('easing applies to the directed progress', () => {
  const a = new Animation({ from: 0, to: 100, duration: 100, easing: (p) => p * p });
  a.play(0);
  eq(a.sample(50), 25);
  const b = new Animation({
    from: 0,
    to: 100,
    duration: 100,
    iterations: 2,
    direction: 'alternate',
    easing: (p) => p * p,
  });
  b.play(0);
  eq(b.sample(150), 25, 'easing sees the directed (reversed) progress');
});

test('setPlaybackRate while running keeps local time continuous', () => {
  const { events, onfinish } = recorder();
  const a = new Animation({ from: 0, to: 100, duration: 1000, onfinish });
  a.play(0);
  a.setPlaybackRate(2, 500);
  eq(a.sample(500), 50, 'no jump at the rate change');
  eq(a.sample(750), 100, 'finishes at 2x');
  eq(events[0], 750);
});

test('setPlaybackRate while paused applies on resume', () => {
  const { events, onfinish } = recorder();
  const a = new Animation({ from: 0, to: 100, duration: 1000, onfinish });
  a.play(0);
  a.pause(200);
  a.setPlaybackRate(4, 200);
  a.resume(1000);
  eq(a.sample(1000), 20, 'still frozen at resume');
  eq(a.sample(1200), 100, '200ms at 4x covers the remaining 800ms');
  eq(events[0], 1200);
});

test('seeking a finished animation re-arms it', () => {
  const { events, onfinish } = recorder();
  const a = new Animation({ from: 0, to: 100, duration: 100, onfinish });
  a.play(0);
  eq(a.sample(100), 100);
  eq(events.length, 1);
  a.seek(0, 200);
  eq(a.state, 'paused');
  eq(a.sample(300), 0, 'held at the seeked local time');
  a.resume(300);
  eq(a.sample(400), 100);
  eq(events.length, 2, 'finishes and notifies again');
  eq(events[1], 400);
});

test('localTime reflects the piecewise mapping', () => {
  const a = new Animation({ from: 0, to: 100, duration: 1000 });
  eq(a.localTime(0), undefined, 'idle');
  a.play(100);
  eq(a.localTime(100), 0);
  eq(a.localTime(600), 500);
  a.pause(600);
  eq(a.localTime(900), 500);
});
