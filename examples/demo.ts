import { Store, type Command, VersionConflictError, CommandFailedError } from '../src/index.ts';

interface Ledger {
  balances: Record<string, number>;
  journal: string[];
}

const log = (msg: string) => console.log(msg);

function debit(account: string, amount: number): Command<Ledger> {
  return {
    name: `debit(${account}, ${amount})`,
    execute(s) {
      if ((s.balances[account] ?? 0) < amount) throw new Error('insufficient funds');
      s.balances[account] -= amount;
      s.journal.push(`-${amount} ${account}`);
    },
    undo(s) {
      s.balances[account] += amount;
      s.journal.push(`+${amount} ${account} (rollback)`);
    },
  };
}

function credit(account: string, amount: number): Command<Ledger> {
  return {
    name: `credit(${account}, ${amount})`,
    execute(s) {
      s.balances[account] = (s.balances[account] ?? 0) + amount;
      s.journal.push(`+${amount} ${account}`);
    },
    undo(s) {
      s.balances[account] -= amount;
      s.journal.push(`-${amount} ${account} (rollback)`);
    },
  };
}

const store = new Store<Ledger>({ balances: { alice: 100, bob: 50 }, journal: [] });

log('== 1. 组合命令原子提交（转账 = 借 + 贷，单条历史） ==');
store.commit([debit('alice', 30), credit('bob', 30)]);
log(`balances: ${JSON.stringify(store.getState().balances)}, version: ${store.getVersion()}`);

log('\n== 2. 中间命令失败，已执行部分回滚 ==');
try {
  store.commit([debit('alice', 20), debit('bob', 9999), credit('alice', 5)]);
} catch (err) {
  const e = err as CommandFailedError;
  log(`failed: ${e.commandName}, rolled back: [${e.rolledBack}]`);
}
log(`balances unchanged: ${JSON.stringify(store.getState().balances)}, version: ${store.getVersion()}`);

log('\n== 3. 撤销：逆操作反向执行，组合逆操作完整 ==');
store.commit([debit('alice', 10), credit('bob', 10), credit('bob', 5)]);
log(`before undo: ${JSON.stringify(store.getState().balances)}`);
store.undo();
log(`after undo:  ${JSON.stringify(store.getState().balances)}`);

log('\n== 4. 撤销前状态已改变 -> 冲突而非误改 ==');
const stale = store.commit([credit('alice', 7)]);
store.commit([credit('alice', 1)]);
try {
  store.undoCommit(stale.id);
} catch (err) {
  log(`conflict: ${(err as VersionConflictError).message}`);
}
log(`balances untouched: ${JSON.stringify(store.getState().balances)}`);

log('\n== 5. 分支历史互不覆盖 ==');
store.branch('feature');
store.checkout('feature');
store.commit([credit('carol', 200)]);
log(`feature: ${JSON.stringify(store.getState().balances)}`);
store.checkout('main');
log(`main:    ${JSON.stringify(store.getState().balances)} (carol 不存在)`);
log(`main history:    [${store.history('main').map((c) => c.id)}]`);
log(`feature history: [${store.history('feature').map((c) => c.id)}]`);

log('\n== 6. 状态可回放：replay 与当前状态一致 ==');
store.checkout('feature');
log(`replayed == committed: ${JSON.stringify(store.replay()) === JSON.stringify(store.getState())}`);

log('\n== 7. 读者看不到事务中间态 ==');
const seenDuringTx: number[] = [];
const peek: Command<Ledger> = {
  name: 'peek',
  execute() {
    seenDuringTx.push(store.getState().balances['alice']);
  },
  undo() {},
};
store.commit([debit('alice', 5), peek, debit('alice', 5)]);
log(`reader saw alice=${seenDuringTx[0]} during tx, committed alice=${store.getState().balances['alice']}`);
