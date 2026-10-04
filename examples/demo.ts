import { MemoryLogger, ModuleLoader } from "../src/index.ts";

const logger = new MemoryLogger();
const loader = new ModuleLoader({ logger });

loader.define("config", () => ({ dbUrl: "sqlite://demo.db" }));
loader.define("db", (ctx) => {
  const config = ctx.require<{ dbUrl: string }>("config");
  ctx.onDispose(() => console.log("  (db 连接已关闭)"));
  return { query: (sql: string) => `[${config.dbUrl}] ${sql}` };
});
loader.define("repo", (ctx) => {
  const db = ctx.require<{ query: (sql: string) => string }>("db");
  return { findAll: () => db.query("SELECT * FROM users") };
});

console.log("== 场景 1：依赖装载与逆序清理 ==");
const repo = loader.load<{ findAll: () => string }>("repo");
console.log("  查询结果:", repo.instance.findAll());
const again = loader.load("repo");
console.log("  重复装载共享实例:", repo.instance === again.instance);
again();
repo();

console.log("\n== 场景 2：旧释放函数晚调用 ==");
const stale = loader.load("config");
stale();
const fresh = loader.load("config");
stale();
console.log("  旧释放函数调用后，新实例仍存活:", loader.isLoaded("config"));
fresh();

console.log("\n== 场景 3：装载失败回滚 ==");
loader.define("fragile", (ctx) => {
  ctx.require("db");
  throw new Error("证书过期");
});
try {
  loader.load("fragile");
} catch (error) {
  console.log("  捕获:", (error as Error).message);
}

console.log("\n== 场景 4：依赖循环 ==");
loader.define("a", (ctx) => (ctx.require("b"), {}));
loader.define("b", (ctx) => (ctx.require("a"), {}));
try {
  loader.load("a");
} catch (error) {
  console.log("  捕获:", (error as Error).message);
}

console.log("\n== 完整生命周期日志 ==");
console.log(logger.render());
