import {
  formatPath,
  merge3,
  resolveAll,
  type Conflict,
  type Resolution,
} from "../src/index.ts";

const base = {
  meta: { version: 1, title: "团队看板" },
  users: [
    { id: "u1", name: "Alice", role: "admin" },
    { id: "u2", name: "Bob", role: "member" },
  ],
  settings: { theme: "light", pageSize: 20 },
};

const ours = {
  meta: { version: 2, title: "团队看板" },
  users: [
    { id: "u2", name: "Bob", role: "member" },
    { id: "u3", name: "Carol", role: "member" },
  ],
  settings: { theme: "dark", pageSize: 50 },
};

const theirs = {
  meta: { version: 3, title: "Team Board" },
  users: [
    { id: "u1", name: "Alice", role: "owner" },
    { id: "u2", name: "Bob", role: "member" },
  ],
  settings: { theme: "dark", pageSize: 30 },
};

const { merged, conflicts } = merge3(base, ours, theirs);

console.log("=== 冲突列表（三方值） ===");
for (const c of conflicts) {
  console.log(`\n${formatPath(c.path)}  [${c.reason}]`);
  console.log(`  base:   ${JSON.stringify(c.base)}`);
  console.log(`  ours:   ${JSON.stringify(c.ours)}`);
  console.log(`  theirs: ${JSON.stringify(c.theirs)}`);
}

console.log("\n=== 默认合并结果（冲突处取 ours） ===");
console.log(JSON.stringify(merged, null, 2));

const decisions = new Map<string, Resolution>([
  ["$.meta.version", { take: "theirs" }],
  ["$.users[0]", { take: "theirs" }],
  ["$.settings.pageSize", { value: 100 }],
]);

const final = resolveAll(merged, conflicts, (c: Conflict) => {
  const key = formatPath(c.path);
  return decisions.get(key) ?? { take: "ours" };
});

console.log("\n=== 人工逐项解决后的最终结果 ===");
console.log(JSON.stringify(final, null, 2));
