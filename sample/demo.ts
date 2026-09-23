import {
  ConflictError,
  PreconditionFailedError,
  Store,
  TransactionRolledBackError,
} from "../src/store.ts";
import {
  failing,
  insert,
  remove,
  replace,
  type TextBuffer,
} from "../src/text-commands.ts";

const show = (label: string, store: Store<TextBuffer>) =>
  console.log(
    `${label}\n  -> 分支=${store.currentBranch} 版本=${store.version} 状态=${JSON.stringify(store.read())}`,
  );

const store = new Store<TextBuffer>({ text: "", edits: 0 });

console.log("=== 1. 多命令原子提交 ===");
store.commit([
  insert(0, "hello", 0), // 前置版本：命令 1 作用于版本 0
  insert(5, " world", 1), // 命令 2 作用于版本 1
  replace(0, 5, "Hello", 2),
]);
show("一次提交 3 个命令", store);

console.log("\n=== 2. 读者看不到事务中间态 ===");
const seenDuringTxn: string[] = [];
store.commit([
  {
    name: "spy",
    expectedVersion: 3,
    execute(state) {
      seenDuringTxn.push(store.read().text); // 事务中读取：仍是提交前状态
      return { name: "spy", state, undo: (s) => s };
    },
  },
  insert(11, "!", 4),
]);
console.log(`  事务中读到的状态: ${JSON.stringify(seenDuringTxn[0])}（旧状态）`);
show("提交后读者看到", store);

console.log("\n=== 3. 中间命令失败，回滚已执行部分 ===");
try {
  store.commit([
    insert(0, "[临时]", 5),
    failing("boom"),
    insert(0, "不会执行"),
  ]);
} catch (e) {
  console.log(`  捕获: ${(e as TransactionRolledBackError).message}`);
}
show("回滚后状态不变", store);

console.log("\n=== 4. 前置版本不满足 ===");
try {
  store.commit([insert(0, "x", 999)]);
} catch (e) {
  console.log(`  捕获: ${(e as PreconditionFailedError).message}`);
}

console.log("\n=== 5. 撤销：逆操作反向应用 ===");
store.undo();
show("撤销事务 2 后", store);
store.undo();
show("撤销事务 1 后", store);

console.log("\n=== 6. 撤销前状态被改变 -> 冲突而非误改 ===");
store.commit([insert(0, "abc", 0)]);
store.restore({ text: "外部并发写入", edits: 99 }, 42); // 模拟并发写
try {
  store.undo();
} catch (e) {
  console.log(`  捕获: ${(e as ConflictError).message}`);
}
show("冲突后状态保持外部值", store);

console.log("\n=== 7. 分支历史互不覆盖 ===");
store.restore({ text: "abc", edits: 1 }, 1); // 恢复正常线
store.createBranch("feature");
store.checkout("feature");
store.commit([remove(0, 3, 1), insert(0, "feature 分支", 2)]);
show("feature 分支提交", store);
store.undo();
show("feature 分支撤销", store);
store.checkout("main");
show("切回 main（历史未被覆盖）", store);
console.log(`  main 历史长度=${store.historySize("main")}，feature 历史长度=${store.historySize("feature")}`);

console.log("\n=== 8. 状态可回放 ===");
for (const name of store.branchNames()) {
  const replayed = store.replay(name);
  store.checkout(name);
  const same =
    JSON.stringify(replayed.state) === JSON.stringify(store.read()) &&
    replayed.version === store.version;
  console.log(`  分支 ${name}: 回放结果与当前状态一致 = ${same}`);
}
