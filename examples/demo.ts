import { Pseudonymizer, REDACTED, type LogEntry } from "../src/index.ts";

// 日志只含路径/域名/假名，绝不含原值
const logs: LogEntry[] = [];
const logger = (e: LogEntry) => {
  logs.push(e);
  console.log(`  [${e.level}] ${e.code} ${e.path ?? ""}`);
};

const engine = new Pseudonymizer({
  secret: process.env.DEMO_SECRET ?? "demo-secret-rotate-me",
  rules: [
    { pattern: "users[].id", action: "pseudonymize", domain: "user.id" },
    { pattern: "users[].email", action: "pseudonymize", domain: "pii.email" },
    { pattern: "users[].country", action: "allow" }, // 显式授权才允许原样透传
    { pattern: "orders[].userId", action: "pseudonymize", domain: "user.id" }, // 外键：同域同假名，仍可关联
    { pattern: "orders[].items[].supplierEmail", action: "pseudonymize", domain: "pii.email" },
  ],
  unauthorizedPolicy: "error", // 未授权字段：记录错误并脱敏，绝不透传
  logger,
});

const input = {
  users: [
    { id: "u-1", email: "alice@example.com", country: "SE", password: "hunter2" },
    { id: "u-2", email: "bob@example.com", country: "NO", password: "hunter3" },
  ],
  orders: [
    { userId: "u-1", items: [{ supplierEmail: "alice@example.com" }, { supplierEmail: "carol@example.com" }] },
    { userId: "u-2", items: [{ supplierEmail: "bob@example.com" }] },
  ],
};

console.log("== 处理过程日志 ==");
const { data, issues, stats } = engine.pseudonymize(input);

console.log("\n== 脱敏数据（可安全导出，不含原值）==");
console.log(JSON.stringify(data, null, 2));

console.log("\n== 映射表（单独导出，须另行加密保管）==");
const mapping = engine.exportMapping();
console.log(JSON.stringify(mapping, null, 2));

console.log("\n== 问题（均定位到具体路径）==");
console.log(JSON.stringify(issues, null, 2));

console.log("\n== 统计 ==");
console.log(stats);

// 验证：外键关联仍然成立
const d = data as any;
const userIds = new Set(d.users.map((u: any) => u.id));
console.log("\n== 校验 ==");
console.log("外键关联保持:", d.orders.every((o: any) => userIds.has(o.userId)));
console.log("同域同值同假名:", d.users[0].email === d.orders[0].items[0].supplierEmail);
console.log("未授权字段已脱敏:", d.users[0].password === REDACTED && d.users[1].password === REDACTED);
console.log("脱敏数据不含原值:", !JSON.stringify(data).includes("alice@example.com") && !JSON.stringify(data).includes("hunter"));
console.log("日志不含原值:", !JSON.stringify(logs).includes("alice@example.com") && !JSON.stringify(logs).includes("hunter"));
