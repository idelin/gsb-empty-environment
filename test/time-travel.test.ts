import { test } from "node:test";
import assert from "node:assert/strict";
import {
  signal,
  computed,
  effect,
  TimeTravelController,
} from "../src/index.ts";

test("验收: 3次修改 -> Undo 2步 -> 精准回到第1步 -> Redo 1步无漂移", () => {
  const controller = new TimeTravelController();
  const count = controller.register(signal(0));
  const name = controller.register(signal("a"));
  const double = computed(() => count.get() * 2);
  const label = computed(() => `${name.get()}:${count.get()}`);

  controller.transaction("step-1", () => {
    count.set(1);
  });
  controller.transaction("step-2", () => {
    count.set(2);
    name.set("b");
  });
  controller.transaction("step-3", () => {
    count.set(3);
  });

  assert.equal(count.get(), 3);
  assert.equal(name.get(), "b");
  assert.equal(double.get(), 6);
  assert.equal(label.get(), "b:3");

  assert.equal(controller.undo(2), true);

  assert.equal(count.get(), 1, "count 应恢复到第1步");
  assert.equal(name.get(), "a", "name 应恢复到第1步");
  assert.equal(double.get(), 2, "computed double 应精准恢复");
  assert.equal(label.get(), "a:1", "computed label 应精准恢复");

  assert.equal(controller.redo(1), true);

  assert.equal(count.get(), 2, "redo 后 count 应无漂移");
  assert.equal(name.get(), "b", "redo 后 name 应无漂移");
  assert.equal(double.get(), 4, "redo 后 computed double 应无漂移");
  assert.equal(label.get(), "b:2", "redo 后 computed label 应无漂移");

  assert.equal(controller.redo(1), true);
  assert.equal(count.get(), 3);
  assert.equal(double.get(), 6);
  assert.equal(controller.redo(1), false, "到达最新节点后 redo 应返回 false");
});

test("Undo/Redo 自动触发 effect 重新渲染同步", () => {
  const controller = new TimeTravelController();
  const count = controller.register(signal(0));
  const renders: number[] = [];
  effect(() => {
    renders.push(count.get());
  });

  controller.transaction("s1", () => count.set(1));
  controller.transaction("s2", () => count.set(2));
  controller.transaction("s3", () => count.set(3));
  assert.deepEqual(renders, [0, 1, 2, 3]);

  controller.undo(2);
  assert.deepEqual(renders, [0, 1, 2, 3, 1], "undo 应触发一次重渲染到快照值");

  controller.redo(1);
  assert.deepEqual(renders, [0, 1, 2, 3, 1, 2], "redo 应触发一次重渲染到快照值");
});

test("快照不可变: 冻结且不受后续外部突变影响", () => {
  const controller = new TimeTravelController();
  const user = controller.register(signal({ name: "alice", tags: ["x"] }));

  controller.transaction("rename", () => {
    user.set({ name: "bob", tags: ["x", "y"] });
  });

  const snap = controller.currentSnapshot;
  const stored = snap.values.get(user.id) as { name: string; tags: string[] };
  assert.equal(Object.isFrozen(stored), true);
  assert.equal(Object.isFrozen(stored.tags), true);
  assert.throws(() => {
    stored.name = "mallory";
  }, TypeError);

  controller.undo(1);
  const live = user.get();
  assert.equal(live.name, "alice");
  live.tags.push("hacked");
  controller.redo(1);
  assert.equal(user.get().name, "bob");
  assert.deepEqual(user.get().tags, ["x", "y"], "快照不受 live 对象突变污染");
});

test("Undo 后新修改产生分支, Redo 沿新分支前进", () => {
  const controller = new TimeTravelController();
  const count = controller.register(signal(0));

  controller.transaction("s1", () => count.set(1));
  controller.transaction("s2", () => count.set(2));
  controller.undo(2);
  controller.transaction("alt", () => count.set(99));

  assert.equal(count.get(), 99);
  assert.equal(controller.redo(1), false, "新分支顶端无 redo 目标");

  controller.undo(1);
  assert.equal(count.get(), 0);
  assert.equal(controller.redo(1), true, "redo 应沿最近访问的分支前进");
  assert.equal(count.get(), 99);
});

test("jumpTo 跳转至任意历史快照", () => {
  const controller = new TimeTravelController();
  const count = controller.register(signal(0));
  const ids: number[] = [controller.currentSnapshot.id];

  for (const v of [10, 20, 30]) {
    controller.transaction(`set-${v}`, () => count.set(v));
    ids.push(controller.currentSnapshot.id);
  }

  assert.equal(controller.jumpTo(ids[1]), true);
  assert.equal(count.get(), 10);

  assert.equal(controller.jumpTo(ids[3]), true);
  assert.equal(count.get(), 30);

  assert.equal(controller.jumpTo(ids[0]), true);
  assert.equal(count.get(), 0);

  assert.equal(controller.jumpTo(9999), false, "不存在的快照 id 应返回 false");
});

test("多 Signal 单事务只产生一个快照", () => {
  const controller = new TimeTravelController();
  const a = controller.register(signal(0));
  const b = controller.register(signal(0));

  controller.transaction("both", () => {
    a.set(1);
    b.set(2);
  });

  assert.equal(controller.history().length, 2, "initial + 1 个事务快照");
  controller.undo(1);
  assert.equal(a.get(), 0);
  assert.equal(b.get(), 0);
  assert.equal(controller.canUndo, false);
});
