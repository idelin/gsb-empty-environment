import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  InspectionService,
  SinkHost,
  TaintHost,
  TaintViolation,
  anchoringVerdict,
} from "../src/index.ts";

describe("未锚定正则不能清洗污点", () => {
  it("未锚定捕获沿用污点，并在事实链上记录 capture", () => {
    const host = new TaintHost();
    const input = host.source("user-input", "hello; rm -rf /");

    const result = host.capture(input, /(\w+)/);
    assert.equal(result.matched, true);
    assert.equal(result.cleansed, false);

    const group = result.groups[1].value;
    assert.equal(group.raw, "hello");
    assert.equal(group.isTainted, true);
    const labels = group.chain().map((step) => step.label);
    assert.match(labels[0], /^capture\(/);
    assert.match(labels[1], /^origin\(user-input/);
  });

  it("含 .* 的锚定模式同样拒绝清洗", () => {
    const host = new TaintHost();
    const input = host.source("arg", "anything");

    const result = host.capture(input, /^(.*)$/);
    assert.equal(result.cleansed, false);
    assert.equal(result.match?.isTainted, true);
    assert.equal(anchoringVerdict(/^(.*)$/).reason, "pattern-contains-.*");
  });

  it("m 标志使 ^/$ 退化为行边界，拒绝清洗", () => {
    const host = new TaintHost();
    const input = host.source("arg", "x\ny");

    const result = host.capture(input, /^([a-z]+)$/m);
    assert.equal(result.cleansed, false);
    assert.equal(result.groups[1].value.isTainted, true);
  });
});

describe("已锚定白名单捕获清洗污点", () => {
  it("^…$ 且不含 .* 的捕获组为干净值", () => {
    const host = new TaintHost();
    const input = host.source("user-input", "alice-42");

    const result = host.capture(input, /^([a-z]+)-(\d+)$/);
    assert.equal(result.cleansed, true);
    assert.equal(result.groups[1].value.raw, "alice");
    assert.equal(result.groups[1].value.isTainted, false);
    assert.equal(result.groups[2].value.raw, "42");
    assert.equal(result.groups[2].value.isTainted, false);
  });

  it("清洗后的白名单值可安全进入 system", () => {
    const host = new TaintHost();
    const sinks = new SinkHost(host);
    const input = host.source("user-input", "ls");

    const cleansed = host.capture(input, /^([a-z]+)$/).groups[1].value;
    const outcome = sinks.system(cleansed);
    assert.deepEqual(outcome, { sink: "system", command: "ls", allowed: true });
  });
});

describe("拼接/插值/join 传播与拼接回污点", () => {
  it("清洗值与污点值拼接后重新带污点（拼接回污点）", () => {
    const host = new TaintHost();
    const safe = host
      .capture(host.source("name", "alice"), /^([a-z]+)$/)
      .groups[1].value;
    assert.equal(safe.isTainted, false);

    const rejoined = host.concat("echo ", safe);
    // 干净拼接仍干净
    assert.equal(rejoined.isTainted, false);

    // 攻击者把未清洗的原始污点再拼回去，污点随拼接复活
    const taintedSuffix = host.source("user-input", "; rm -rf /");
    const attacked = host.concat(rejoined, taintedSuffix);
    assert.equal(attacked.isTainted, true);
    const labels = attacked.chain().map((step) => step.label);
    assert.match(labels[0], /^concat\(/);
    assert.match(labels[1], /^origin\(user-input/);
  });

  it("插值字符串任一插值位污点则整体污点", () => {
    const host = new TaintHost();
    const name = host.source("name", "bob");
    const cmd = host.interpolate`greet ${name} now`;
    assert.equal(cmd.raw, "greet bob now");
    assert.equal(cmd.isTainted, true);
    assert.match(cmd.chainString(), /interpolate.*->.*origin\(name/);
  });

  it("join：任一元素污点则结果污点，分隔符污点也传播", () => {
    const host = new TaintHost();
    const clean = host.box("a");
    const dirty = host.source("field", "b");
    const array = host.array([clean, dirty]);

    const joined = host.join("-", array);
    assert.equal(joined.raw, "a-b");
    assert.equal(joined.isTainted, true);

    const allClean = host.array(["x", "y"]);
    const dirtySep = host.source("sep", ",");
    const withDirtySep = host.join(dirtySep, allClean);
    assert.equal(withDirtySep.raw, "x,y");
    assert.equal(withDirtySep.isTainted, true);
  });
});

describe("数组元素与赋值传播", () => {
  it("下标赋值传播右值污点，取出元素保留来源", () => {
    const host = new TaintHost();
    const array = host.array(["x"]);
    const dirty = host.source("post", "data");

    array.set(1, dirty);
    assert.equal(array.get(0).isTainted, false);
    assert.equal(array.get(1).raw, "data");
    assert.equal(array.get(1).isTainted, true);

    const copied = host.assign(array.get(1));
    assert.equal(copied.isTainted, true);
    const labels = copied.chain().map((step) => step.label);
    assert.match(labels[0], /^assign\(/);
    assert.match(labels[1], /^origin\(post/);
  });

  it("substr 不清洗：子串沿用原串污点", () => {
    const host = new TaintHost();
    const input = host.source("blob", "secret-data");
    const part = host.substr(input, 0, 6);
    assert.equal(part.raw, "secret");
    assert.equal(part.isTainted, true);
    assert.match(part.chainString(), /substr.*->.*origin\(blob/);
  });
});

describe("哈希值传播", () => {
  it("哈希值随赋值传播，干净键访问取回污点值", () => {
    const host = new TaintHost();
    const hash = host.hash();
    const dirty = host.source("env-HOME", "/home/attacker");

    hash.set("home", dirty);
    const fetched = hash.get("home");
    assert.equal(fetched.raw, "/home/attacker");
    assert.equal(fetched.isTainted, true);
    assert.match(fetched.chainString(), /origin\(env-HOME/);
  });

  it("污点键访问给结果额外挂上 hash-get 来源", () => {
    const host = new TaintHost();
    const hash = host.hash();
    hash.set("k", "v");
    const taintedKey = host.source("key-input", "k");

    const fetched = hash.get(taintedKey);
    assert.equal(fetched.raw, "v");
    assert.equal(fetched.isTainted, true);
    assert.match(fetched.chainString(), /hash-get.*->.*origin\(key-input/);
  });
});

describe("危险宿主与来源链", () => {
  it("污点进入 system/exec/反引号均抛出含来源链的 TaintViolation", () => {
    const host = new TaintHost();
    const sinks = new SinkHost(host);
    const input = host.source("cgi-param", "rm -rf /");
    const cmd = host.concat("sh -c ", input);

    for (const invoke of [
      () => sinks.system(cmd),
      () => sinks.exec(cmd),
      () => sinks.backtick(cmd),
    ]) {
      assert.throws(invoke, (error: unknown) => {
        assert.ok(error instanceof TaintViolation);
        assert.ok(["system", "exec", "backtick"].includes(error.sink));
        assert.equal(error.origins[0].source, "cgi-param");
        const labels = error.chain.map((step) => step.label);
        assert.match(labels[0], /^concat\(/);
        assert.match(labels[1], /^origin\(cgi-param/);
        return true;
      });
    }
  });

  it("多来源拼接的来源链可枚举全部根来源", () => {
    const host = new TaintHost();
    const left = host.source("a", "1");
    const right = host.source("b", "2");
    const merged = host.concat(left, right);

    assert.equal(merged.isTainted, true);
    const rootNames = merged.roots().map((origin) => origin.source);
    assert.deepEqual(rootNames.sort(), ["a", "b"]);
  });

  it("污点路径进入写模式 open 抛出 open-write 违规，读模式放行", () => {
    const host = new TaintHost();
    const sinks = new SinkHost(host);
    const dirtyPath = host.source("upload-name", "../../etc/passwd");

    assert.throws(
      () => sinks.open(host.concat("> ", dirtyPath)),
      (error: unknown) => {
        assert.ok(error instanceof TaintViolation);
        assert.equal(error.sink, "open-write");
        assert.match(error.value.chainString(), /concat.*->.*origin\(upload-name/);
        return true;
      },
    );

    const readOnly = sinks.open(host.concat("< ", dirtyPath));
    assert.equal(readOnly.allowed, true);
    assert.equal(readOnly.mode, "<");
  });

  it("InspectionService 只检查不抛异常，并能委托 guard", () => {
    const service = new InspectionService();
    const input = service.host.source("query", "x");
    const report = service.report(input);
    assert.equal(report.tainted, true);
    assert.equal(report.origins[0].source, "query");
    assert.equal(service.isTainted(service.host.box("clean")), false);

    assert.throws(
      () => service.guard("system", input),
      (error: unknown) => error instanceof TaintViolation,
    );
  });
});
