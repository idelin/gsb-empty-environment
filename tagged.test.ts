import test from "node:test";
import assert from "node:assert/strict";
import {
  clone,
  createUntagger,
  deriveString,
  isTagged,
  mergeArrays,
  project,
  rawOf,
  tag,
  tagsOf,
} from "./tagged.ts";

const sorted = (tags: ReadonlySet<string>): string[] => [...tags].sort();

test("构造保留标签，重复打标取并集", () => {
  const v = tag("secret", "pii");
  assert.equal(rawOf(v), "secret");
  assert.deepEqual(sorted(tagsOf(v)), ["pii"]);

  const v2 = tag(v, "auth", "pii");
  assert.equal(rawOf(v2), "secret");
  assert.deepEqual(sorted(tagsOf(v2)), ["auth", "pii"]);

  assert.deepEqual(sorted(tagsOf("plain")), []);
  assert.ok(isTagged(v));
  assert.ok(!isTagged("plain"));
});

test("复制保留所有层级的标签且结构独立", () => {
  const original = tag(
    { token: tag("abc", "secret"), list: [tag(1, "n")] },
    "pii",
  );
  const copied = clone(original);

  assert.deepEqual(sorted(tagsOf(copied)), ["pii"]);
  const raw = rawOf(copied) as { token: unknown; list: unknown[] };
  assert.deepEqual(sorted(tagsOf(raw.token)), ["secret"]);
  assert.deepEqual(sorted(tagsOf(raw.list[0])), ["n"]);

  assert.notEqual(copied, original);
  assert.notEqual(rawOf(copied), rawOf(original));
  (rawOf(original) as { extra?: string }).extra = "changed";
  assert.equal((rawOf(copied) as { extra?: string }).extra, undefined);
});

test("复制对循环结构安全", () => {
  const node: { self?: unknown; name: string } = { name: "n" };
  node.self = node;
  const copied = clone(tag(node, "x"));
  const raw = rawOf(copied) as { self: unknown };
  assert.equal(raw.self, raw);
  assert.deepEqual(sorted(tagsOf(copied)), ["x"]);
});

test("数组合并保留元素标签并取数组标签并集", () => {
  const a = tag([tag(1, "elem")], "listA");
  const b = tag([2], "listB");
  const merged = mergeArrays(a, b, [3]);

  assert.deepEqual(sorted(tagsOf(merged)), ["listA", "listB"]);
  const items = rawOf(merged);
  assert.equal(items.length, 3);
  assert.deepEqual(sorted(tagsOf(items[0])), ["elem"]);
  assert.equal(rawOf(items[0]), 1);
  assert.equal(items[1], 2);
  assert.equal(items[2], 3);

  assert.throws(() => mergeArrays(tag("nope", "x")), TypeError);
});

test("字段投影保留来源标签与字段自身标签", () => {
  const record = tag(
    {
      name: tag("alice", "pii"),
      password: tag("hunter2", "secret"),
      id: 7,
    },
    "record",
  );
  const picked = project(record, ["name", "id"] as const);

  assert.deepEqual(sorted(tagsOf(picked)), ["record"]);
  const raw = rawOf(picked);
  assert.deepEqual(Object.keys(raw).sort(), ["id", "name"]);
  assert.deepEqual(sorted(tagsOf(raw.name)), ["pii"]);
  assert.equal(raw.id, 7);
});

test("派生字符串取所有输入标签的并集", () => {
  const derived = deriveString(
    tag("alice", "pii"),
    "@",
    tag("example.com", "domain"),
    tag(42, "id", "pii"),
  );
  assert.equal(rawOf(derived), "alice@example.com42");
  assert.deepEqual(sorted(tagsOf(derived)), ["domain", "id", "pii"]);
});

test("只有白名单去标函数可移除指定标签", () => {
  const untag = createUntagger(["pii"]);
  const value = tag(
    { a: tag(1, "pii"), b: tag(2, "pii", "secret") },
    "pii",
    "audit",
  );

  const cleaned = untag(value, "pii");
  assert.deepEqual(sorted(tagsOf(cleaned)), ["audit"]);
  const raw = rawOf(cleaned) as { a: unknown; b: unknown };
  assert.deepEqual(sorted(tagsOf(raw.a)), []);
  assert.deepEqual(sorted(tagsOf(raw.b)), ["secret"]);

  assert.deepEqual(sorted(tagsOf(value)), ["audit", "pii"]);

  assert.throws(() => untag(value, "secret"), /whitelist/);

  const strictUntag = createUntagger([]);
  assert.throws(() => strictUntag(value, "pii"), /whitelist/);
});

test("其他操作不会丢失标签", () => {
  const value = tag({ s: tag("x", "secret") }, "secret");
  assert.deepEqual(sorted(tagsOf(clone(value))), ["secret"]);
  assert.deepEqual(sorted(tagsOf(project(value, ["s"] as const))), ["secret"]);
  assert.deepEqual(sorted(tagsOf(mergeArrays(tag([value], "secret")))), ["secret"]);
  assert.deepEqual(sorted(tagsOf(deriveString(value))), ["secret"]);
  assert.deepEqual(sorted(tagsOf(tag(value))), ["secret"]);
});
