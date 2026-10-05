# versioned-merge-store

三副本版本归并库（TypeScript）：同一键可离线修改，事件用向量时钟标记，
支持支配判断、并发版本保留与显式解决。

## 核心语义

- **向量时钟**（src/vectorClock.ts）：compare 给出 before / after / equal / concurrent
  四种偏序关系；dominates 即“已见”判断。
- **并发版本保留**（src/store.ts）：同一键下只保留两两并发的版本，
  被支配的版本立即回收。
- **显式解决**：get 返回所见版本与上下文时钟 context；
  resolve(key, value, context) 的新时钟 = context + 本副本自增一次。
  因此解决恰好覆盖调用者见过的全部版本（随事件传播在全网回收），
  而与 context 并发的版本（调用者未见）在本地和远端都不会被误删。
- **幂等归并**：applyEvent 纯靠时钟支配关系去重与回收，
  交换、重复、乱序传播均收敛到同一版本集合。

## 运行测试

需要 Node >= 23.6（或 >= 22.6 加 --experimental-strip-types），无第三方依赖：

    node --test test/*.test.ts

测试覆盖（test/convergence.test.ts）：

- 向量时钟的支配 / 并发 / 相等 / 归并
- 三副本离线并发改同一键，并发版本全部保留
- 显式解决覆盖全部已见版本，传播后全网单版本
- 解决不误删调用者未见的并发版本
- 交换（gossip）、重复投递、乱序投递（含解决事件先到）最终集合一致
