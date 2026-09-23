import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ReplayStore } from '../src/replay-store.ts';
import type { Commit, RecordedEffect, RecordedEvent } from '../src/types.ts';

interface CounterState {
  count: number;
}

interface CounterCommand {
  steps: number;
  failAfterFirstStateChange?: boolean;
}

type CounterEvent = RecordedEvent & { amount: number };
type CounterEffect = RecordedEffect;

const initialState: CounterState = Object.freeze({ count: 0 });

test('effects run once for a live dispatch and never for history replay', async () => {
  let id = 0;
  let liveRequests = 0;
  const live = new ReplayStore<CounterState, CounterCommand, CounterEvent, CounterEffect>({
    initialState,
    createId: () => `c${++id}`,
    now: () => '2026-01-01T00:00:00.000Z',
    reduce: (state, event) => ({ count: state.count + event.amount }),
    handle: (command) => ({
      events: Array.from({ length: command.steps }, () => ({ type: 'increment', amount: 1 })),
      effects: [{ type: 'simulated-request' }],
    }),
    runEffect: async () => {
      liveRequests += 1;
    },
  });

  await live.dispatch({ steps: 2 });
  assert.equal(liveRequests, 1);

  live.checkout('c1');
  live.state();
  live.events();
  live.log();
  assert.equal(liveRequests, 1);

  let replayRequests = 0;
  const replay = new ReplayStore<CounterState, CounterCommand, CounterEvent, CounterEffect>({
    initialState,
    history: live.history(),
    reduce: (state, event) => ({ count: state.count + event.amount }),
    handle: () => ({
      events: [{ type: 'increment', amount: 1 }],
      effects: [{ type: 'simulated-request' }],
    }),
    runEffect: async () => {
      replayRequests += 1;
    },
  });

  assert.equal(replay.state('main').count, 2);
  assert.equal(replayRequests, 0);

  await replay.dispatch({ steps: 1 });
  assert.equal(replayRequests, 1);
  assert.equal(replay.state('main').count, 3);
});

test('a historical branch does not overwrite the live branch history', async () => {
  let id = 0;
  const store = new ReplayStore<CounterState, CounterCommand, CounterEvent, CounterEffect>({
    initialState,
    createId: () => `c${++id}`,
    reduce: (state, event) => ({ count: state.count + event.amount }),
    handle: (command) => ({
      events: Array.from({ length: command.steps }, () => ({
        type: 'increment',
        amount: 1,
      })),
      effects: [],
    }),
  });

  const first = await store.dispatch({ steps: 2 });
  const second = await store.dispatch({ steps: 1 });
  assert.equal(second.commit.id, 'c2');

  store.checkout(first.commit.id);
  await assert.rejects(store.dispatch({ steps: 1 }), /detached historical commit/);

  store.branch('repair', first.commit.id);
  const branched = await store.dispatch({ steps: 1 });
  assert.equal(branched.state.count, 3);

  store.checkout('main');
  const continued = await store.dispatch({ steps: 1 });
  assert.equal(continued.state.count, 4);

  assert.deepEqual(store.log('repair').map((commit) => commit.id), ['c3', 'c1']);
  assert.deepEqual(store.log('main').map((commit) => commit.id), ['c4', 'c2', 'c1']);
  assert.equal(store.state('repair').count, 3);
  assert.equal(store.state('main').count, 4);
});

test('a failed multi-event transaction leaves no commit, state change, or effect', async () => {
  let effects = 0;
  let observedSecondState: CounterState | undefined;
  const store = new ReplayStore<CounterState, CounterCommand, CounterEvent, CounterEffect>({
    initialState,
    createId: () => 'failed-commit',
    reduce: (state, event) => {
      if (event.type === 'boom') throw new Error('second state change failed');
      const next = { count: state.count + event.amount };
      observedSecondState = next;
      return next;
    },
    handle: (command) => ({
      events: command.failAfterFirstStateChange
        ? [{ type: 'increment', amount: 1 }, { type: 'boom', amount: 0 }]
        : [{ type: 'increment', amount: 1 }],
      effects: [{ type: 'simulated-request' }],
    }),
    runEffect: async () => {
      effects += 1;
    },
  });

  await assert.rejects(
    store.dispatch({ steps: 1, failAfterFirstStateChange: true }),
    /second state change failed/
  );

  assert.equal(observedSecondState?.count, 1);
  assert.equal(store.state().count, 0);
  assert.deepEqual(store.log(), []);
  assert.equal(effects, 0);
});

test('all events in one dispatch are stored as one atomic commit', async () => {
  const store = new ReplayStore<CounterState, CounterCommand, CounterEvent, CounterEffect>({
    initialState,
    createId: () => 'atomic',
    reduce: (state, event) => ({ count: state.count + event.amount }),
    handle: () => ({
      events: [
        { type: 'increment', amount: 2 },
        { type: 'increment', amount: 3 },
      ],
      effects: [],
    }),
  });

  const result = await store.dispatch({ steps: 2 });
  const commits: Commit<CounterEvent, CounterEffect>[] = [...store.log()];

  assert.equal(commits.length, 1);
  assert.equal(commits[0]?.events.length, 2);
  assert.equal(result.state.count, 5);
});
