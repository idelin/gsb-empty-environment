import test from "node:test";
import assert from "node:assert/strict";
import { route, Route } from "../src/route.ts";

function roundTrip(r: Route, params: Record<string, string>, expectedUrl?: string) {
  const url = r.build(params);
  if (expectedUrl !== undefined) assert.equal(url, expectedUrl);
  assert.deepEqual(r.match(url), params);
  return url;
}

test("父子路径组合", () => {
  const users = route("/users");
  const user = users.extend("/:id");
  const posts = user.extend("/posts/:postId");
  assert.equal(posts.path, "/users/:id/posts/:postId");
  roundTrip(posts, { id: "1", postId: "42" }, "/users/1/posts/42");
  // 组合不修改原路由
  assert.equal(users.path, "/users");
  roundTrip(user, { id: "7" }, "/users/7");
});

test("重复参数名：单模式内拒绝", () => {
  assert.throws(() => route("/:id/posts/:id"), /Duplicate parameter name ":id"/);
});

test("重复参数名：extend 组合后的有效路径上拒绝", () => {
  const parent = route("/org/:id");
  assert.throws(() => parent.extend("/members/:id"), /Duplicate parameter name ":id"/);
  // 不同名则允许
  assert.equal(parent.extend("/members/:memberId").path, "/org/:id/members/:memberId");
});

test("重复参数名：可选父段与子段同名同样拒绝", () => {
  assert.throws(() => route("/org/:orgId?").extend("/:orgId"), /Duplicate/);
});

test("可选父段省略时，子参数仍有独立来源", () => {
  const members = route("/org/:orgId?").extend("/members/:memberId");
  // 父可选段省略：子参数照常取自自己的段
  roundTrip(members, { memberId: "m1" }, "/org/members/m1");
  const parsed = members.match("/org/members/m1");
  assert.deepEqual(parsed, { memberId: "m1" });
  assert.ok(!("orgId" in parsed!));
  // 父段出现：两者各自独立
  roundTrip(members, { orgId: "o1", memberId: "m1" }, "/org/o1/members/m1");
  // 子参数值与父静态段同文本也不串位
  roundTrip(members, { memberId: "members" }, "/org/members/members");
  roundTrip(members, { orgId: "members", memberId: "m1" }, "/org/members/members/m1");
});

test("参数往返：必填 + 可选混合", () => {
  const r = route("/users/:id/posts/:postId?");
  roundTrip(r, { id: "1", postId: "9" }, "/users/1/posts/9");
  roundTrip(r, { id: "1" }, "/users/1/posts");
  assert.deepEqual(r.match("/users/1/posts"), { id: "1" });
});

test("参数往返：根路由", () => {
  const root = route("/");
  roundTrip(root, {}, "/");
  assert.deepEqual(root.match("/"), {});
  assert.deepEqual(root.match(""), {});
});

test("空串规则：必填参数空串抛错，可选参数空串视为省略", () => {
  const required = route("/users/:id");
  assert.throws(() => required.build({ id: "" }), /must not be an empty string/);
  assert.throws(() => required.build({}), /Missing required parameter/);

  const optional = route("/users/:id?");
  assert.equal(optional.build({ id: "" }), "/users");
  assert.equal(optional.build({}), "/users");
  assert.deepEqual(optional.match("/users"), {});
});

test("未知参数与非字符串参数抛错", () => {
  const r = route("/users/:id");
  assert.throws(() => r.build({ id: "1", extra: "x" }), /Unknown parameter "extra"/);
  assert.throws(
    () => r.build({ id: 1 as unknown as string }),
    /must be a string/,
  );
});

test("保留字符：逐个段 percent-encode，往返无损", () => {
  const r = route("/files/:name");
  roundTrip(r, { name: "a/b" }, "/files/a%2Fb");
  roundTrip(r, { name: "a?b#c" }, "/files/a%3Fb%23c");
  roundTrip(r, { name: "100%" }, "/files/100%25");
  roundTrip(r, { name: "a b&c=d" }, "/files/a%20b%26c%3Dd");
  roundTrip(r, { name: "张三/笔记" }, "/files/%E5%BC%A0%E4%B8%89%2F%E7%AC%94%E8%AE%B0");
  // 编码后的斜杠不会被当成段分隔符
  assert.deepEqual(r.match("/files/a%2Fb"), { name: "a/b" });
  // 非法百分号序列不匹配
  assert.equal(r.match("/files/%zz"), null);
  assert.equal(r.match("/files/100%"), null);
});

test("保留字符：多参数各自编码互不干扰", () => {
  const r = route("/:a/:b");
  roundTrip(r, { a: "x/y", b: "y?z" }, "/x%2Fy/y%3Fz");
});

test("尾斜杠规则", () => {
  // 模式中的尾斜杠被忽略
  assert.equal(route("/users/").path, "/users");
  assert.equal(route("/users/:id/").path, "/users/:id");
  // build 永不产出尾斜杠
  assert.equal(route("/users/:id").build({ id: "1" }), "/users/1");
  // 匹配时允许并忽略尾部斜杠
  const r = route("/users/:id");
  assert.deepEqual(r.match("/users/1/"), { id: "1" });
  assert.deepEqual(r.match("/users/1//"), { id: "1" });
  // 中间的空段（双斜杠）不匹配
  assert.equal(r.match("/users//1"), null);
  // 前导斜杠可有可无
  assert.deepEqual(r.match("users/1"), { id: "1" });
});

test("匹配时忽略 query 与 hash", () => {
  const r = route("/users/:id");
  assert.deepEqual(r.match("/users/1?tab=info#top"), { id: "1" });
});

test("歧义：连续可选段在构造时拒绝", () => {
  assert.throws(() => route("/:a?/:b?"), /consecutive optional segments/);
  assert.throws(() => route("/x/:a?/:b?/y"), /consecutive optional segments/);
});

test("歧义：可选参数值撞上后续静态段时 build 抛错", () => {
  const r = route("/:a?/x/:b?");
  // 正常值往返无碍
  roundTrip(r, {}, "/x");
  roundTrip(r, { a: "1" }, "/1/x");
  roundTrip(r, { b: "2" }, "/x/2");
  roundTrip(r, { a: "1", b: "2" }, "/1/x/2");
  // a="x" 会产出 "/x/x"，反解析得到 { b: "x" } —— 必须拒绝
  assert.throws(() => r.build({ a: "x" }), /Ambiguous route/);
});

test("不匹配的路径返回 null", () => {
  const r = route("/users/:id");
  assert.equal(r.match("/posts/1"), null);
  assert.equal(r.match("/users"), null);
  assert.equal(r.match("/users/1/extra"), null);
});
