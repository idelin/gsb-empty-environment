import {
  accountCount,
  averageTransfer,
  bankReducer,
  createMockTransferClient,
  handleBankCommand,
  initialBankState,
  totalBalance,
  transferCount,
  type BankCommand,
  type BankEffect,
  type BankEvent,
  type BankState,
} from '../src/bank.ts';
import { ReplayStore } from '../src/replay-store.ts';

let sequence = 0;
let tick = 0;

const liveClient = createMockTransferClient();
const store = new ReplayStore<BankState, BankCommand, BankEvent, BankEffect>({
  initialState: initialBankState,
  reduce: bankReducer,
  handle: handleBankCommand,
  runEffect: liveClient.run,
  createId: () => `c${++sequence}`,
  now: () => `2026-01-01T00:0${tick++}:00.000Z`,
});

async function run(): Promise<void> {
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

  console.log('live main:', store.state().accounts, 'requests =', liveClient.requestCount());

  store.checkout('c2');
  console.log(
    'history c2:',
    store.state().accounts,
    'accounts =',
    store.derive(accountCount),
    'transfers =',
    store.derive(transferCount),
    'requests =',
    liveClient.requestCount()
  );

  store.branch('historical-fix', 'c2');
  await store.dispatch({ type: 'deposit', accountId: 'alice', amount: 25 });
  console.log('historical-fix:', store.state().accounts, 'requests =', liveClient.requestCount());

  store.checkout('main');
  await store.dispatch({ type: 'deposit', accountId: 'alice', amount: 5 });
  console.log('main continued:', store.state().accounts, 'requests =', liveClient.requestCount());

  const snapshot = store.history();
  const replayClient = createMockTransferClient();
  const restored = new ReplayStore<BankState, BankCommand, BankEvent, BankEffect>({
    initialState: initialBankState,
    reduce: bankReducer,
    handle: handleBankCommand,
    runEffect: replayClient.run,
    history: snapshot,
  });

  console.log(
    'restored main:',
    restored.state().accounts,
    'average transfer =',
    restored.derive(averageTransfer),
    'requests =',
    replayClient.requestCount()
  );

  restored.checkout('historical-fix');
  console.log(
    'restored branch:',
    restored.state().accounts,
    'total =',
    restored.derive(totalBalance),
    'requests =',
    replayClient.requestCount()
  );
}

run().catch((error: unknown) => {
  console.error(error);
  process.exitCode = 1;
});
