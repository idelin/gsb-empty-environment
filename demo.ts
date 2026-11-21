import { InspectionService, SinkHost, TaintHost, TaintViolation } from "./src/index.ts";

const host = new TaintHost();
const sinks = new SinkHost(host);

// 1) 未锚定正则：捕获沿用污点
const input = host.source("cgi-param", "hello; rm -rf /");
const loose = host.capture(input, /(\w+)/);
console.log("unanchored capture tainted =", loose.groups[1].value.isTainted);
console.log("  chain:", loose.groups[1].value.chainString());

// 2) 锚定白名单：^…$ 且无 .*，捕获被清洗
const safe = host.capture(host.source("name", "alice-42"), /^([a-z]+)-(\d+)$/);
console.log(
  "anchored whitelist cleansed =",
  safe.cleansed,
  "g1 tainted =",
  safe.groups[1].value.isTainted,
);

// 3) 拼接回污点：干净值 + 污点值 => 污点复活
const rejoined = host.concat("echo ", safe.groups[1].value);
const attacked = host.concat(rejoined, input);
console.log("re-concat tainted =", attacked.isTainted);

// 4) 哈希值传播
const hash = host.hash();
hash.set("home", host.source("env-HOME", "/home/attacker"));
console.log("hash value tainted =", hash.get("home").isTainted);

// 5) 危险宿主拦截并给出来源链
try {
  sinks.system(attacked);
} catch (error) {
  const violation = error as TaintViolation;
  console.log("blocked sink =", violation.sink);
  console.log("  origins =", violation.origins.map((origin) => origin.source).join(","));
  console.log(
    "  chain:",
    violation.chain.map((step) => `#${step.factId} ${step.label}`).join("\n         "),
  );
}

// 检查服务门面
const service = new InspectionService(host);
console.log("service report tainted =", service.report(attacked).tainted);
