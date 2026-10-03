import {
  signal,
  computed,
  effect,
  TimeTravelController,
} from "../src/index.ts";

const controller = new TimeTravelController();
const count = controller.register(signal(0));
const name = controller.register(signal("a"));
const double = computed(() => count.get() * 2);
const label = computed(() => `${name.get()}:${count.get()}`);

effect(() => {
  console.log(`[render] count=${count.get()} name=${name.get()} double=${double.get()} label=${label.get()}`);
});

console.log("--- 3 次用户修改 ---");
controller.transaction("step-1", () => count.set(1));
controller.transaction("step-2", () => {
  count.set(2);
  name.set("b");
});
controller.transaction("step-3", () => count.set(3));

console.log("--- Undo 2 步 (期望回到 step-1: count=1 name=a) ---");
controller.undo(2);

console.log("--- Redo 1 步 (期望回到 step-2: count=2 name=b) ---");
controller.redo(1);

console.log("--- 历史路径 ---");
for (const snap of controller.history()) {
  console.log(`#${snap.id} ${snap.label} ->`, Object.fromEntries(snap.values));
}
