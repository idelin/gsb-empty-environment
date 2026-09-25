import {
  LinkFailure,
  ModuleCache,
  ModuleRegistry,
  RuleBook,
  buildDepGraphs,
  makeAttrs,
  shortestCycle,
  type ModuleSource,
} from "../src/index.ts";

const line = (s: string) => console.log(s);

// ---------- 世界 v1：菱形依赖（app -> authz/audit -> geo） ----------
function worldV1(): ModuleSource[] {
  const geo: ModuleSource = {
    name: "geo",
    version: "1.0.0",
    imports: [],
    exports: [
      { name: "regions", kind: "decl", signature: "string[]", decl: ["cn-sh", "cn-bj", "us-west"] },
      { name: "defaultRegion", kind: "value", signature: "string", make: () => "cn-sh" },
    ],
  };
  const authz: ModuleSource = {
    name: "authz",
    version: "1.0.0",
    imports: [{ module: "geo", version: "1.0.0", names: [{ name: "regions" }], loc: "authz.ts:1:1" }],
    exports: [
      {
        name: "denyOutside",
        kind: "value",
        signature: "rule",
        make: (ctx) => ({
          type: "rule",
          effect: "deny",
          condition: { op: "not", arg: { op: "in", attr: "region", set: ctx.get("regions") as string[] } },
        }),
      },
    ],
  };
  const audit: ModuleSource = {
    name: "audit",
    version: "1.0.0",
    imports: [{ module: "geo", version: "1.0.0", names: [{ name: "defaultRegion" }], loc: "audit.ts:1:1" }],
    exports: [
      { name: "homeRegion", kind: "value", signature: "string", make: (ctx) => ctx.get("defaultRegion") as string },
    ],
  };
  const app: ModuleSource = {
    name: "app",
    version: "1.0.0",
    imports: [
      { module: "authz", version: "1.0.0", names: [{ name: "denyOutside" }], loc: "app.ts:1:1" },
      { module: "audit", version: "1.0.0", names: [{ name: "homeRegion" }], loc: "app.ts:2:1" },
    ],
    exports: [],
  };
  return [geo, authz, audit, app];
}

// ---------- 世界 v2：geo 增加区域，依赖它的模块版本级联升级 ----------
function worldV2(): ModuleSource[] {
  const [geo, authz, audit, app] = worldV1();
  return [
    {
      ...geo,
      version: "1.1.0",
      exports: [
        { name: "regions", kind: "decl", signature: "string[]", decl: ["cn-sh", "cn-bj", "us-west", "eu-central"] },
        geo.exports[1],
      ],
    },
    {
      ...authz,
      version: "1.0.1",
      imports: [{ module: "geo", version: "1.1.0", names: [{ name: "regions" }], loc: "authz.ts:1:1" }],
    },
    {
      ...audit,
      version: "1.0.1",
      imports: [{ module: "geo", version: "1.1.0", names: [{ name: "defaultRegion" }], loc: "audit.ts:1:1" }],
    },
    {
      ...app,
      imports: [
        { module: "authz", version: "1.0.1", names: [{ name: "denyOutside" }], loc: "app.ts:1:1" },
        { module: "audit", version: "1.0.1", names: [{ name: "homeRegion" }], loc: "app.ts:2:1" },
      ],
    },
  ];
}

const registry = new ModuleRegistry();

line("=== 1. 链接世界 v1（菱形共享导入） ===");
const snap1 = registry.hotReplace(worldV1());
line(`快照 #${snap1.seq} 版本向量: ${snap1.vector}`);
line(`authz 依赖版本向量: ${snap1.get("authz")?.versionVector}`);

const { declGraph, evalGraph } = buildDepGraphs(worldV1());
line(`声明环: ${JSON.stringify(shortestCycle(declGraph))}，求值环: ${JSON.stringify(shortestCycle(evalGraph))}`);

line("");
line("=== 2. 基于快照 #1 的规则决策 ===");
const book1 = RuleBook.fromSnapshot(snap1);
const euAttrs = makeAttrs({ region: "eu-central", level: 2 });
line(`规则: ${book1.list().map((r) => r.qualifiedName).join(", ")}`);
line(`region=eu-central 决策(v1): ${book1.evaluate("authz:denyOutside", euAttrs) ?? "allow"}`);

line("");
line("=== 3. 热替换到世界 v2，旧快照继续存活 ===");
const snap2 = registry.hotReplace(worldV2());
line(`快照 #${snap2.seq} 版本向量: ${snap2.vector}`);
const book2 = RuleBook.fromSnapshot(snap2);
line(`旧快照#region=eu-central 决策: ${book1.evaluate("authz:denyOutside", euAttrs) ?? "allow"}（仍用旧区域表）`);
line(`新快照#region=eu-central 决策: ${book2.evaluate("authz:denyOutside", euAttrs) ?? "allow"}（新区域表已含 eu-central）`);

line("");
line("=== 4. 缓存按完整依赖版本向量隔离 ===");
const cache = new ModuleCache();
const authz1 = snap1.get("authz");
const authz2 = snap2.get("authz");
if (authz1 && authz2) {
  cache.set(authz1, "regions:size", 3);
  line(`v1 向量缓存命中: ${cache.get(authz1, "regions:size")}`);
  line(`v2 向量缓存命中: ${String(cache.get(authz2, "regions:size"))}（不同向量，互不混用）`);
}

line("");
line("=== 5. 失败的热替换：初始化失败，不允许部分换版 ===");
const broken: ModuleSource[] = [
  {
    name: "geo",
    version: "2.0.0",
    imports: [],
    exports: [{ name: "regions", kind: "decl", signature: "string[]", decl: [] }],
    init: () => {
      throw new Error("geo@2.0.0 配置损坏");
    },
  },
];
try {
  registry.hotReplace(broken);
} catch (e) {
  line(`热替换被拒绝:\n${(e as LinkFailure).message}`);
}
line(`注册表仍是快照 #${registry.snapshot().seq}: ${registry.snapshot().vector}`);

line("");
line("=== 6. 错误来源链演示 ===");
try {
  registry.hotReplace([
    {
      name: "x",
      version: "1.0.0",
      imports: [{ module: "geo", version: "1.1.0", names: [{ name: "ghost" }], loc: "x.ts:4:2" }],
      exports: [],
    },
    ...worldV2().slice(0, 1),
  ]);
} catch (e) {
  line(`缺失导出定位:\n${(e as LinkFailure).message}`);
}
const cycleWorld: ModuleSource[] = [
  { name: "p", version: "1.0.0", imports: [{ module: "q", version: "1.0.0", names: [{ name: "v" }], loc: "p.ts:1:1" }], exports: [{ name: "v", kind: "value", signature: "number", make: () => 1 }] },
  { name: "q", version: "1.0.0", imports: [{ module: "p", version: "1.0.0", names: [{ name: "v" }], loc: "q.ts:1:1" }], exports: [{ name: "v", kind: "value", signature: "number", make: () => 1 }] },
];
try {
  registry.hotReplace(cycleWorld);
} catch (e) {
  line(`求值环拒绝:\n${(e as LinkFailure).message}`);
}
line(`最终注册表仍是快照 #${registry.snapshot().seq}（失败不换版）`);
