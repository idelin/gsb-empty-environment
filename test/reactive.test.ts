import { test } from "node:test";
import assert from "node:assert/strict";
import { signal, computed, effect, batch } from "../src/reactive.ts";

test("Signal：读写与相同值去重", () => {
  const count = signal(0);
  assert.equal(count.value, 0);

  let runs = 0;
  effect(() => {
    count.value;
    runs++;
  });
  assert.equal(runs, 1);

  count.value = 1;
  assert.equal(runs, 2);

  count.value = 1; // 相同值不触发
  assert.equal(runs, 2);

  count.update((n) => n + 1);
  assert.equal(count.value, 2);
  assert.equal(runs, 3);
});

test("Computed：惰性求值与缓存", () => {
  const a = signal(2);
  let runs = 0;
  const doubled = computed(() => {
    runs++;
    return a.value * 2;
  });

  assert.equal(runs, 0); // 未读取不计算
  assert.equal(doubled.value, 4);
  assert.equal(runs, 1);
  assert.equal(doubled.value, 4);
  assert.equal(runs, 1); // 缓存命中

  a.value = 3;
  assert.equal(doubled.value, 6);
  assert.equal(runs, 2);
});

test("Effect：立即执行、响应变更、dispose 停止", () => {
  const name = signal("a");
  const seen: string[] = [];
  const stop = effect(() => {
    seen.push(name.value);
  });
  assert.deepEqual(seen, ["a"]);

  name.value = "b";
  assert.deepEqual(seen, ["a", "b"]);

  stop();
  name.value = "c";
  assert.deepEqual(seen, ["a", "b"]);
});

test("Effect：cleanup 在重跑与 dispose 时调用", () => {
  const a = signal(0);
  const log: string[] = [];
  const stop = effect(() => {
    const v = a.value;
    log.push(`run:${v}`);
    return () => log.push(`cleanup:${v}`);
  });
  a.value = 1;
  stop();
  assert.deepEqual(log, ["run:0", "cleanup:0", "run:1", "cleanup:1"]);
});

test("动态依赖：条件分支切换后旧分支不再触发", () => {
  const flag = signal(true);
  const x = signal("x");
  const y = signal("y");
  const picked = computed(() => (flag.value ? x.value : y.value));

  const seen: string[] = [];
  effect(() => {
    seen.push(picked.value);
  });
  assert.deepEqual(seen, ["x"]);

  flag.value = false;
  assert.deepEqual(seen, ["x", "y"]);

  x.value = "x2"; // 已退订，不再触发
  assert.deepEqual(seen, ["x", "y"]);

  y.value = "y2";
  assert.deepEqual(seen, ["x", "y", "y2"]);
});

test("batch：多个信号写入只结算一次", () => {
  const first = signal("Ada");
  const last = signal("Lovelace");
  let runs = 0;
  effect(() => {
    first.value;
    last.value;
    runs++;
  });
  assert.equal(runs, 1);

  batch(() => {
    first.value = "Grace";
    last.value = "Hopper";
  });
  assert.equal(runs, 2);
});

test("链式 Computed：按拓扑序逐层拉取", () => {
  const a = signal(1);
  const b = computed(() => a.value + 1);
  const c = computed(() => b.value + 1);
  const d = computed(() => c.value + 1);

  assert.equal(d.value, 4);
  a.value = 10;
  assert.equal(d.value, 13);
  assert.equal(b.value, 11);
});
