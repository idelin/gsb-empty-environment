# virtual-periodic-scheduler

虚拟时钟驱动的周期任务调度器（TypeScript）。时钟只由调用方（测试）通过
`advance` 推进，任务可以跑得比周期更久。

## 语义

- **固定起点调度**：触发点锚定在 `schedule` 时刻的时间网格
  （`anchor + k * period`），任务耗时不影响网格，不产生漂移。
- **漏触发合并补偿**：任务超时运行期间漏掉的任意多个触发，只合并为
  **一次** 补偿运行（`kind: "catch-up"`），在任务完成的虚拟时刻立即执行。
- **回到原网格**：补偿运行结束后，下一个触发点是当前时刻之后的下一个
  网格点；补偿运行自身不再累计漏触发（不会链式补偿）。
- **暂停不累计**：`pause()` 期间不产生也不累计触发；`resume()` 将网格
  按暂停时长平移（保持相位），暂停前已漏的触发在恢复时仍只补一次。
- **停止**：`stop()` 取消未来所有触发；正在执行的回调会自然跑完，
  之后不再补偿、不再调度。
- **失败**：任务抛异常或 Promise reject 时通过 `onError` 上报，
  调度照常继续（补偿与网格均不受影响）。

## API

```ts
const clock = new VirtualClock();
const scheduler = new Scheduler(clock);
const handle = scheduler.schedule(
  async (info) => { /* info.kind: "grid" | "catch-up" */ },
  100,                       // 周期（虚拟毫秒）
  { onError: (err) => {} },  // 可选
);
handle.pause(); handle.resume(); handle.stop();
handle.state;      // "active" | "paused" | "stopped"
handle.executing;  // 回调是否正在执行

clock.advance(1000); // 只有 advance 能推进时间
clock.now();
```

## 运行测试

环境内无网络，TypeScript 编译器以符号链接指向本机 yarn 全局目录：

```sh
npm test   # 编译到 dist/ 并运行全部测试
```
