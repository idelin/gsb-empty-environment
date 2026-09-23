# time-travel-store

无响应式框架依赖的时间旅行状态库（TypeScript，零依赖，Node.js 内置测试）。

## 特性

- **纯状态操作可回放**：`defineOp` 注册的 reducer 是纯函数，操作名 + 参数记入提交，回放时按提交链重放。
- **副作用不重复执行**：`emit` 登记的副作用只在实时提交时执行一次，结果写入提交日志；`checkout` 回放、查看历史均不触发。
- **事务原子性**：`transact` 内所有操作作用于草稿状态，全部成功后一次性提交；提交前 `getState()`/`derived()` 返回旧状态，监听器只通知一次；抛错整体回滚。
- **派生值按依赖重算**：`defineDerived(name, deps, compute)` 以顶层状态切片为依赖，引用不变走缓存；回到历史时按历史状态重算。
- **分支历史完整**：在历史提交上产生新操作时自动分叉 `fork-N`，原实时分支历史不被覆盖；`switchBranch`/`backToLive` 切回实时后继续新操作。

## 运行

```bash
node src/example.ts     # 运行样例（Node >= 22.6，直接执行 TypeScript）
node --test test/store.test.ts   # 或 npm test
```

## API 摘要

```ts
const store = new Store<State>(initialState);
store.defineOp('inc', (s, n) => ({ ...s, count: s.count + n })); // 纯操作
store.defineEffect('http', fakeHttp);                            // 副作用处理器
store.defineDerived('double', ['count'], (s) => s.count * 2);    // 派生值

store.dispatch('inc', 1);                                        // 单操作提交
store.transact(() => {                                           // 原子事务
  store.run('inc', 1);
  store.emit('http', 'POST', '/sync');                           // 仅实时执行一次
});

store.checkout(commitId);   // 回到历史：只重放纯操作
store.backToLive();         // 回到当前分支尖端（实时模式）
store.switchBranch('main'); // 切分支并落在尖端
store.history('main');      // 完整提交链（含副作用结果，查阅不触发副作用）
```

## 运行样例输出

```
== 1. 实时操作（每个提交触发一次模拟 HTTP 副作用） ==
三次提交后:
  mode=live branch=main head=c3 | todos=[[1,"买牛奶",true],[2,"写报告",false]] filter=active | remaining=1 visible=1 | httpRequests=3

== 2. 回到历史（只重放纯操作，不重发请求） ==
checkout c1（只有 1 个 todo，filter=all）:
  mode=replay branch=main head=c1 | todos=[[1,"买牛奶",false]] filter=all | remaining=1 visible=1 | httpRequests=3
  回看提交记录的副作用结果（不触发请求）: [{"kind":"http","args":["POST","/todos"],"result":{"status":200,"requestId":1}}]
  httpRequests 仍为 3（回放期间没有新请求）

== 3. 在历史点上产生新操作 -> 自动分叉，不覆盖 main ==
分叉提交后:
  mode=live branch=fork-1 head=c4 | todos=[[1,"买牛奶",false],[2,"历史分支上的新任务",false]] filter=all | remaining=2 visible=2 | httpRequests=4
  分支列表: [{"name":"main","tip":"c3"},{"name":"fork-1","tip":"c4"}]

== 4. 切回 main 实时模式，继续新操作 ==
switchBranch(main) 后:
  mode=live branch=main head=c3 | ... | httpRequests=4
main 上再提交后:
  mode=live branch=main head=c5 | ... | httpRequests=5

== 5. 两条分支历史均完整 ==
  main: c1(ops=addTodo) <- c2(ops=addTodo) <- c3(ops=toggleTodo,setFilter) <- c5(ops=addTodo)
  fork-1: c1(ops=addTodo) <- c4(ops=addTodo)

派生值重算次数: remaining=5 visible=5
总 httpRequests=5（= 实时提交次数，回放未产生请求）
```

## 测试结果

```
✔ 事务原子性：读者看不到中间态，监听器只通知一次
✔ 事务抛错时整体回滚，无部分提交
✔ 回到历史只重放纯状态变更，副作用不重复执行
✔ 回看提交记录不发送模拟请求，副作用结果来自提交日志
✔ 派生值按历史依赖重算：无关依赖变化不触发重算
✔ 在历史提交上产生新操作会分叉，且不覆盖原实时历史
✔ 切回实时后恢复新操作，两条分支历史均完整
✔ 状态操作可回放：同一操作序列重放得到一致状态
ℹ tests 8  ℹ pass 8  ℹ fail 0
```
