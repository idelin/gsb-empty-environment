import assert from "node:assert/strict";
import test from "node:test";
import {
  ConflictError,
  PreconditionFailedError,
  Store,
  TransactionRolledBackError,
  type Command,
} from "../src/store.ts";
import {
  failing,
  insert,
  remove,
  replace,
  type TextBuffer,
} from "../src/text-commands.ts";

const fresh = () => new Store<TextBuffer>({ text: "", edits: 0 });

test("多命令组合为一次原子提交，版本逐命令递增", () => {
  const store = fresh();
  const record = store.commit([
    insert(0, "hello", 0),
    insert(5, " world", 1),
    replace(0, 5, "Hello", 2),
  ]);
  assert.equal(record.fromVersion, 0);
  assert.equal(record.toVersion, 3);
  assert.equal(store.version, 3);
  assert.deepEqual(store.read(), { text: "Hello world", edits: 3 });
});

test("读者看不到事务中间态，只能看到提交前状态", () => {
  const store = fresh();
  store.commit([insert(0, "abc", 0)]);
  const seen: string[] = [];
  const spy: Command<TextBuffer> = {
    name: "spy",
    expectedVersion: 1,
    execute(state) {
      seen.push(store.read().text);
      return { name: "spy", state, undo: (s) => s };
    },
  };
  store.commit([spy, insert(3, "def", 2)]);
  assert.deepEqual(seen, ["abc"]); // 事务进行中读到的仍是旧状态
  assert.equal(store.read().text, "abcdef"); // 提交后一次性可见
});

test("读者拿到的是冻结快照，无法篡改内部状态", () => {
  const store = fresh();
  store.commit([insert(0, "abc", 0)]);
  const snapshot = store.read();
  assert.throws(() => {
    (snapshot as { text: string }).text = "hacked";
  }, TypeError);
  assert.equal(store.read().text, "abc");
});

test("中间命令失败：反向回滚已执行部分，状态与版本不变", () => {
  const store = fresh();
  store.commit([insert(0, "base", 0)]);
  const undoOrder: string[] = [];
  const tracked = (name: string, version: number): Command<TextBuffer> => ({
    name,
    expectedVersion: version,
    execute: (state) => ({
      name,
      state: { ...state, text: state.text + `+${name}` },
      undo: (s) => {
        undoOrder.push(name);
        return { ...s, text: s.text.replace(`+${name}`, "") };
      },
    }),
  });
  let err: TransactionRolledBackError | undefined;
  try {
    store.commit([tracked("A", 1), tracked("B", 2), failing("boom")]);
  } catch (e) {
    err = e as TransactionRolledBackError;
  }
  assert.ok(err instanceof TransactionRolledBackError);
  assert.equal(err.failedCommand, "boom");
  assert.equal(err.rolledBackCount, 2);
  assert.deepEqual(undoOrder, ["B", "A"]); // 回滚顺序反向
  assert.deepEqual(store.read(), { text: "base", edits: 1 });
  assert.equal(store.version, 1);
  assert.equal(store.historySize(), 1); // 失败事务不留痕
});

test("前置版本不满足：拒绝提交且不产生任何副作用", () => {
  const store = fresh();
  store.commit([insert(0, "abc", 0)]);
  assert.throws(
    () => store.commit([insert(0, "x", 99)]),
    (e: unknown) =>
      e instanceof PreconditionFailedError &&
      e.expected === 99 &&
      e.actual === 1,
  );
  assert.deepEqual(store.read(), { text: "abc", edits: 1 });
  assert.equal(store.version, 1);
});

test("事务内部后续命令的前置版本按暂存版本链校验", () => {
  const store = fresh();
  assert.throws(
    () => store.commit([insert(0, "a", 0), insert(1, "b", 5)]),
    PreconditionFailedError,
  );
  assert.equal(store.version, 0); // 整体未生效
});

test("撤销：逆操作按反向顺序完整应用，状态精确还原", () => {
  const store = fresh();
  const undoOrder: string[] = [];
  const tracked = (
    name: string,
    text: string,
    version: number,
  ): Command<TextBuffer> => ({
    name,
    expectedVersion: version,
    execute: (state) => ({
      name,
      state: { ...state, text: state.text + text },
      undo: (s) => {
        undoOrder.push(name);
        return { ...s, text: s.text.replace(text, "") };
      },
    }),
  });
  store.commit([tracked("c1", "-1", 0), tracked("c2", "-2", 1), tracked("c3", "-3", 2)]);
  assert.equal(store.read().text, "-1-2-3");
  store.undo();
  assert.deepEqual(undoOrder, ["c3", "c2", "c1"]); // 撤销顺序反向
  assert.deepEqual(store.read(), { text: "", edits: 0 }); // 逆操作完整：精确回到初始态
  assert.equal(store.version, 0);
});

test("撤销前状态被外部改变：抛冲突而非误改", () => {
  const store = fresh();
  store.commit([insert(0, "abc", 0)]);
  store.restore({ text: "concurrent write", edits: 7 }, 42);
  assert.throws(
    () => store.undo(),
    (e: unknown) =>
      e instanceof ConflictError && e.expected === 1 && e.actual === 42,
  );
  assert.deepEqual(store.read(), { text: "concurrent write", edits: 7 });
  assert.equal(store.historySize(), 1); // 冲突不消费历史
});

test("分支历史互不覆盖：未选分支的提交与撤销互不影响", () => {
  const store = fresh();
  store.commit([insert(0, "main-1", 0)]);
  store.createBranch("feature");

  store.checkout("feature");
  store.commit([insert(6, "+feat", 1)]);
  store.undo(); // feature 上撤销
  assert.equal(store.historySize("feature"), 0);

  store.checkout("main");
  assert.equal(store.historySize("main"), 1); // main 历史未被覆盖
  assert.deepEqual(store.read(), { text: "main-1", edits: 1 });
  assert.equal(store.version, 1);

  store.undo(); // main 上撤销自己的提交
  assert.deepEqual(store.read(), { text: "", edits: 0 });
  store.checkout("feature");
  assert.deepEqual(store.read(), { text: "main-1", edits: 1 }); // feature 基点不受影响
});

test("状态操作可回放：日志重放结果与当前状态一致", () => {
  const store = fresh();
  store.commit([insert(0, "hello", 0), remove(0, 1, 1)]);
  store.commit([replace(0, 4, "HELO", 2)]);
  store.undo();
  store.commit([insert(4, "!", 2)]);
  const replayed = store.replay();
  assert.deepEqual(replayed.state, store.read());
  assert.equal(replayed.version, store.version);
});

test("分支从派生点回放：各自日志独立重建", () => {
  const store = fresh();
  store.commit([insert(0, "base", 0)]);
  store.createBranch("dev");
  store.checkout("dev");
  store.commit([remove(0, 4, 1), insert(0, "dev", 2)]);
  for (const name of ["main", "dev"]) {
    const replayed = store.replay(name);
    store.checkout(name);
    assert.deepEqual(replayed.state, store.read());
    assert.equal(replayed.version, store.version);
  }
});

test("空提交是合法的原子操作", () => {
  const store = fresh();
  const record = store.commit([]);
  assert.equal(record.fromVersion, 0);
  assert.equal(record.toVersion, 0);
  assert.equal(store.historySize(), 1);
  store.undo();
  assert.equal(store.version, 0);
});
