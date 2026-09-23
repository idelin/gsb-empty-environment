import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  accountCount,
  averageTransfer,
  bankReducer,
  createMockTransferClient,
  handleBankCommand,
  initialBankState,
  totalBalance,
  transferCount,
  transferVolume,
  type BankCommand,
  type BankEffect,
  type BankEvent,
  type BankState,
} from '../src/bank.ts';
import { ReplayStore } from '../src/replay-store.ts';

function createStore(runEffect?: (effect: BankEffect) => Promise<void>) {
  let id = 0;
  return new ReplayStore<BankState, BankCommand, BankEvent, BankEffect>({
    initialState: initialBankState,
    reduce: bankReducer,
    handle: handleBankCommand,
    runEffect,
    createId: () => `c${++id}`,
  });
}

async function seedLiveBank(store: ReplayStore<BankState, BankCommand, BankEvent, BankEffect>) {
  const commands: BankCommand[] = [
    { type: 'open', accountId: 'alice' },
    { type: 'open', accountId: 'bob' },
    { type: 'deposit', accountId: 'alice', amount: 100 },
    { type: 'deposit', accountId: 'bob', amount: 40 },
    { type: 'transfer', transferId: 't1', from: 'alice', to: 'bob', amount: 30 },
  ];

  for (const command of commands) {
    await store.dispatch(command);
  }
}

test('bank transfer is committed atomically and derived values use live history', async () => {
  const client = createMockTransferClient();
  const store = createStore(client.run);
  await seedLiveBank(store);

  const transferCommit = store.log()[0];
  assert.deepEqual(
    transferCommit?.events.map((event) => event.type),
    ['funds-debited', 'funds-credited']
  );
  assert.deepEqual(store.state().accounts, { alice: 70, bob: 70 });
  assert.equal(store.derive(totalBalance), 140);
  assert.equal(store.derive(transferCount), 1);
  assert.equal(store.derive(transferVolume), 30);
  assert.equal(store.derive(averageTransfer), 30);
  assert.equal(client.requestCount(), 5);
});

test('checkout only folds state events and recalculates historical derived values', async () => {
  const client = createMockTransferClient();
  const store = createStore(client.run);
  await seedLiveBank(store);

  store.checkout('c2');
  assert.deepEqual(store.state().accounts, { alice: 0, bob: 0 });
  assert.equal(store.derive(accountCount), 2);
  assert.equal(store.derive(transferCount), 0);
  assert.equal(store.derive(transferVolume), 0);
  assert.equal(store.derive(averageTransfer), 0);
  assert.deepEqual(
    store.log().map((commit) => commit.id),
    ['c2', 'c1']
  );
  assert.equal(client.requestCount(), 5);
});

test('a branch started from history preserves both complete histories', async () => {
  const client = createMockTransferClient();
  const store = createStore(client.run);
  await seedLiveBank(store);

  store.checkout('c2');
  store.branch('historical-fix');
  await store.dispatch({ type: 'deposit', accountId: 'alice', amount: 25 });

  assert.deepEqual(store.state().accounts, { alice: 25, bob: 0 });
  assert.deepEqual(store.log('historical-fix').map((commit) => commit.id), ['c6', 'c2', 'c1']);
  assert.deepEqual(store.log('main').map((commit) => commit.id), ['c5', 'c4', 'c3', 'c2', 'c1']);

  store.checkout('main');
  await store.dispatch({ type: 'deposit', accountId: 'alice', amount: 5 });
  assert.deepEqual(store.state().accounts, { alice: 75, bob: 70 });
  assert.equal(store.state('historical-fix').accounts.alice, 25);
  assert.equal(store.derive(transferCount), 1);
  assert.equal(store.derive(transferCount, 'historical-fix'), 0);
  assert.equal(client.requestCount(), 7);
});

test('restored history sends no requests and can resume with new operations', async () => {
  const liveClient = createMockTransferClient();
  const live = createStore(liveClient.run);
  await seedLiveBank(live);

  const replayClient = createMockTransferClient();
  const restored = new ReplayStore<BankState, BankCommand, BankEvent, BankEffect>({
    initialState: initialBankState,
    reduce: bankReducer,
    handle: handleBankCommand,
    runEffect: replayClient.run,
    history: live.history(),
  });

  assert.equal(replayClient.requestCount(), 0);
  assert.deepEqual(restored.state().accounts, { alice: 70, bob: 70 });
  assert.equal(restored.derive(averageTransfer), 30);

  restored.checkout('c2');
  restored.state();
  restored.events();
  restored.log();
  assert.equal(replayClient.requestCount(), 0);

  restored.checkout('main');
  await restored.dispatch({ type: 'deposit', accountId: 'bob', amount: 10 });
  assert.equal(replayClient.requestCount(), 1);
  assert.deepEqual(restored.state().accounts, { alice: 70, bob: 80 });
});

test('invalid transfers do not expose or persist a debit-only intermediate state', async () => {
  const client = createMockTransferClient();
  const store = createStore(client.run);
  await store.dispatch({ type: 'open', accountId: 'alice' });
  await store.dispatch({ type: 'open', accountId: 'bob' });
  await store.dispatch({ type: 'deposit', accountId: 'alice', amount: 10 });

  await assert.rejects(
    store.dispatch({
      type: 'transfer',
      transferId: 'too-large',
      from: 'alice',
      to: 'bob',
      amount: 100,
    }),
    /Insufficient funds/
  );

  assert.deepEqual(store.state().accounts, { alice: 10, bob: 0 });
  assert.deepEqual(store.log().map((commit) => commit.branch), ['main', 'main', 'main']);
  assert.equal(store.derive(transferCount), 0);
  assert.equal(client.requestCount(), 3);
});
