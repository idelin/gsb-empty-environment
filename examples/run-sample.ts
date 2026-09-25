import { anonymize } from "../src/index.ts";

const input = {
  users: [
    {
      id: "u-1001",
      email: "alice@example.com",
      contacts: [{ kind: "backup", email: "alice@example.com" }],
      nickname: "al",
    },
    {
      id: "u-1002",
      email: "bob@example.com",
      contacts: [{ kind: "backup", email: "carol@example.com" }],
      nickname: "bob",
    },
  ],
  orders: [
    { id: "o-5001", userId: "u-1001", total: 99.5 },
    { id: "o-5002", userId: "u-1002", total: 12 },
    { id: "o-5003", userId: "u-1001", total: 40 },
  ],
};

const result = anonymize(input, {
  key: process.env.PSEUDONYM_KEY ?? "demo-only-key",
  rules: [
    { path: "users.*.id", domain: "user-id" },
    { path: "users.*.email", domain: "email" },
    { path: "users.*.contacts.*.email", domain: "email" },
    { path: "users.*.contacts.*.kind", domain: "contact-kind" },
    { path: "orders.*.id", domain: "order-id" },
    { path: "orders.*.userId", domain: "user-id" },
    { path: "orders.*.total", domain: "amount", action: "redact" },
  ],
});

console.log("=== anonymized data (safe to export) ===");
console.log(JSON.stringify(result.data, null, 2));
console.log("\n=== mapping export (store separately, no raw values) ===");
console.log(JSON.stringify(result.mapping, null, 2));
console.log("\n=== errors (paths locate every issue) ===");
console.log(JSON.stringify(result.errors, null, 2));
console.log("\n=== stats ===");
console.log(JSON.stringify(result.stats));

const users = (result.data as any).users;
const orders = (result.data as any).orders;
const joined = orders.every((o: any) => users.some((u: any) => u.id === o.userId));
console.log(`\nforeign-key join preserved: ${joined}`);
