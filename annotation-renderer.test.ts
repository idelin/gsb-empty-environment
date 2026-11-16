import { test } from "node:test";
import assert from "node:assert/strict";

import {
  renderAnnotations,
  validatePath,
  validateSize,
  validateToken,
  verifyConfigStructure,
  verifyDirectiveWhitelist,
  type Annotations,
  type DirectiveTemplates,
} from "./annotation-renderer.ts";

const templates: DirectiveTemplates = {
  "proxy/body-size": { directive: "client_max_body_size", type: "size" },
  "proxy/upstream": { directive: "proxy_pass", type: "path", block: "location /api" },
  "proxy/name": { directive: "server_name", type: "token" },
};

const allowedDirectives = ["client_max_body_size", "proxy_pass", "server_name", "listen"];

const baseConfig = "http {\n    server {\n        listen 80;\n";
const baseClose = "\n    }\n}";

function render(annotations: Annotations) {
  return renderAnnotations(annotations, {
    templates,
    allowedDirectives,
    baseConfig: baseConfig + baseClose,
  });
}

test("合法 size 通过校验并渲染，整份配置块深度回到 0", () => {
  const result = render({ "proxy/body-size": "10m" });
  assert.equal(result.ok, true, JSON.stringify(result.diagnostics));
  assert.match(result.fragment, /^client_max_body_size 10m;$/);
  assert.ok(result.config?.includes("client_max_body_size 10m;"));
  const structure = verifyConfigStructure(result.config ?? "");
  assert.equal(structure.ok, true);
  assert.equal(structure.depth, 0);
});

test("size 接受纯数字和 k 后缀，拒绝非法单位与小数", () => {
  for (const good of ["1024", "512k", "8m", "0"]) {
    assert.equal(validateSize(good), true, `应接受 ${good}`);
  }
  for (const bad of ["10g", "1.5m", "10 px", "m", "10mm", "", " 10m", "10m "]) {
    assert.equal(validateSize(bad), false, `应拒绝 ${bad}`);
  }
});

test("换行加分号的指令注入被拒绝，不会渲染进配置", () => {
  const payload = "10m;\nload_module /tmp/evil.so;\n#";
  const result = render({ "proxy/body-size": payload });
  assert.equal(result.ok, false);
  assert.equal(result.config, null);
  assert.ok(result.diagnostics.some((d) => d.code === "FORBIDDEN_NEWLINE"));
  assert.ok(result.diagnostics.some((d) => d.code === "FORBIDDEN_SEMICOLON"));
  assert.ok(!result.fragment.includes("load_module"));
});

test("右花括号提前闭合块的注入被拒绝", () => {
  const payload = "on } load_module /tmp/evil.so; {";
  const result = render({ "proxy/name": payload });
  assert.equal(result.ok, false);
  assert.equal(result.config, null);
  assert.ok(result.diagnostics.some((d) => d.code === "FORBIDDEN_CLOSE_BRACE"));
  assert.ok(result.diagnostics.some((d) => d.code === "FORBIDDEN_OPEN_BRACE"));
  assert.ok(!result.fragment.includes("load_module"));
});

test("$ 变量引用被任意类型拒绝", () => {
  const byToken = render({ "proxy/name": "$host" });
  assert.equal(byToken.ok, false);
  assert.ok(byToken.diagnostics.some((d) => d.code === "FORBIDDEN_DOLLAR"));

  const byPath = render({ "proxy/upstream": "/$uri/x" });
  assert.equal(byPath.ok, false);
  assert.ok(byPath.diagnostics.some((d) => d.code === "FORBIDDEN_DOLLAR"));
});

test("path 必须以 / 开头且不含 .. 段", () => {
  for (const good of ["/api", "/usr/local/bin", "/a/b/c", "/", "/api?x=1"]) {
    assert.equal(validatePath(good), true, `应接受 ${good}`);
  }
  for (const bad of ["api", "/../etc/passwd", "/a/../../b", "../etc", "/a/../b"]) {
    assert.equal(validatePath(bad), false, `应拒绝 ${bad}`);
  }

  const result = render({ "proxy/upstream": "/../../etc/passwd" });
  assert.equal(result.ok, false);
  assert.ok(result.diagnostics.some((d) => d.code === "INVALID_PATH"));
  assert.equal(result.config, null);
});

test("合法 path 渲染进 location 块并通过结构复核", () => {
  const result = render({ "proxy/upstream": "/api/v2" });
  assert.equal(result.ok, true, JSON.stringify(result.diagnostics));
  assert.match(result.fragment, /location \/api \{\n\s+proxy_pass \/api\/v2;\n\}/);
  assert.equal(verifyConfigStructure(result.config ?? "").depth, 0);
});

test("token 只允许 [A-Za-z0-9._-]", () => {
  for (const good of ["edge-1", "a.b_c", "ABC123", "v1.2.3"]) {
    assert.equal(validateToken(good), true, `应接受 ${good}`);
  }
  for (const bad of ["a b", "a/b", "a@b", "", "a#b", "a\nb"]) {
    assert.equal(validateToken(bad), false, `应拒绝 ${bad}`);
  }
});

test("回车、NUL、反引号与 # 开头内容全部拒绝", () => {
  const cases: Array<[string, string]> = [
    ["a\rb", "FORBIDDEN_CARRIAGE_RETURN"],
    ["a\0b", "FORBIDDEN_NUL"],
    ["a`b", "FORBIDDEN_BACKTICK"],
    ["# comment", "FORBIDDEN_HASH_PREFIX"],
    ["   # indented", "FORBIDDEN_HASH_PREFIX"],
  ];
  for (const [value, code] of cases) {
    const result = render({ "proxy/name": value });
    assert.equal(result.ok, false, `应拒绝 ${JSON.stringify(value)}`);
    assert.ok(result.diagnostics.some((d) => d.code === code), `缺少 ${code}`);
  }
});

test("未知注解键被丢弃并记录 warning，不影响合法渲染", () => {
  const result = render({
    "proxy/body-size": "10m",
    "evil/inject": "1; } load_module x;",
  });
  assert.equal(result.ok, true, JSON.stringify(result.diagnostics));
  const warning = result.diagnostics.find((d) => d.code === "UNKNOWN_ANNOTATION");
  assert.ok(warning);
  assert.equal(warning?.key, "evil/inject");
  assert.ok(!result.fragment.includes("load_module"));
  assert.ok(result.fragment.includes("client_max_body_size 10m;"));
});

test("模板指令名不在白名单时渲染失败", () => {
  const result = renderAnnotations(
    { "proxy/name": "edge" },
    { templates, allowedDirectives: ["listen"] },
  );
  assert.equal(result.ok, false);
  assert.equal(result.config, null);
  assert.ok(result.diagnostics.some((d) => d.code === "DIRECTIVE_NOT_ALLOWED"));
});

test("白名单复核只对未授权指令报错", () => {
  assert.equal(verifyDirectiveWhitelist(["listen"], allowedDirectives).length, 0);
  const bad = verifyDirectiveWhitelist(["load_module"], allowedDirectives);
  assert.equal(bad.length, 1);
  assert.equal(bad[0].code, "DIRECTIVE_NOT_ALLOWED");
});

test("结构状态机拒绝未闭合块、负深度与缺失分号", () => {
  assert.equal(verifyConfigStructure("http {\n  server {\n").ok, false);
  const negative = verifyConfigStructure("}");
  assert.equal(negative.ok, false);
  assert.ok(negative.diagnostics.some((d) => d.code === "UNBALANCED_CLOSE_BRACE"));
  const noSemicolon = verifyConfigStructure("listen 80\n}");
  assert.ok(noSemicolon.diagnostics.some((d) => d.code === "UNTERMINATED_DIRECTIVE"));
  const emptyStatement = verifyConfigStructure(" ;");
  assert.ok(emptyStatement.diagnostics.some((d) => d.code === "EMPTY_STATEMENT"));
  const balanced = verifyConfigStructure("http {\n  listen 80;\n}\n");
  assert.equal(balanced.ok, true);
  assert.equal(balanced.depth, 0);
});

test("引号与注释内的分隔符不会干扰状态机判定", () => {
  const config = 'server {\n  msg "a;b{c}d"; # a comment with ; { }\n}\n';
  const result = verifyConfigStructure(config);
  assert.equal(result.ok, true, JSON.stringify(result.diagnostics));
  assert.equal(result.depth, 0);
});

test("单个非法值被跳过，其余合法值仍可渲染", () => {
  const result = render({
    "proxy/body-size": "10m",
    "proxy/name": "bad\nname",
  });
  assert.equal(result.ok, false);
  assert.equal(result.config, null);
  assert.ok(result.fragment.includes("client_max_body_size 10m;"));
  assert.ok(!result.fragment.includes("server_name"));
});

test("包入口可导入并导出渲染 API", async () => {
  const api = await import("./index.ts");
  assert.equal(typeof api.renderAnnotations, "function");
  assert.equal(typeof api.verifyConfigStructure, "function");
  assert.equal(typeof api.validateSize, "function");
});
