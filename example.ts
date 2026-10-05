/**
 * 示例：页面切换后，旧页面组件不再收到事件。
 * 运行：node example.ts   （Node >= 22.18 或 >= 23.6，原生运行 TypeScript）
 */
import { EventBus, EventScope } from "./event-bus.ts";

type AppEvents = {
  message: { text: string };
  tick: number;
};

const bus = new EventBus<AppEvents>();

// 全局监听器：不随页面销毁
bus.on("message", ({ text }) => console.log(`  [global]   on message: ${text}`));

class PageComponent {
  private readonly name: string;
  private readonly scope: EventScope;
  private readonly controller = new AbortController();

  constructor(name: string) {
    this.name = name;
    this.scope = bus.createScope();

    // 方式一：关联 scope，destroy 时批量注销
    bus.on(
      "message",
      ({ text }) => console.log(`  [${this.name}] render message: ${text}`),
      { scope: this.scope },
    );

    // 方式二：关联 AbortSignal，destroy 时 abort 即注销
    bus.on(
      "tick",
      (n) => console.log(`  [${this.name}] on tick ${n}`),
      { signal: this.controller.signal },
    );
  }

  destroy(): void {
    this.scope.close();      // 注销 scope 关联的全部订阅
    this.controller.abort(); // 注销 signal 关联的订阅
  }
}

console.log("— 挂载 PageA —");
const pageA = new PageComponent("PageA");
console.log(`active subscriptions: ${bus.size}`); // global + PageA*2 = 3

console.log("emit (PageA 存活):");
bus.emit("message", { text: "hello" });
bus.emit("tick", 1);

console.log("\n— 切换页面：销毁 PageA，挂载 PageB —");
pageA.destroy();
const pageB = new PageComponent("PageB");
console.log(`active subscriptions: ${bus.size}`); // 仍是 3，旧组件订阅已释放

console.log("emit (PageA 已销毁，不应再收到):");
bus.emit("message", { text: "after switch" });
bus.emit("tick", 2);

console.log("\n— 异常收集演示 —");
bus.on("tick", () => {
  throw new Error("listener-1 failed");
});
bus.on("tick", (n) => console.log(`  [PageB]    still runs on tick ${n}`));
const errors = bus.emit("tick", 3);
console.log(`collected errors: ${errors.length} -> ${(errors[0] as Error).message}`);

console.log("\n— 卸载全部页面 —");
pageB.destroy();
console.log(`active subscriptions: ${bus.size}`); // 剩 global + 两个 tick 监听器 = 3
