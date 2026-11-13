import { test } from "node:test";
import assert from "node:assert/strict";
import { loadModule } from "./loader.ts";
import { isModuleTypeAllowed } from "./module-type.ts";

const jsonUrl = "https://example.com/data.json";
const jsUrl = "https://example.com/app.js";
const jsonSource = `{"answer": 42}`;
const jsSource = `globalThis.answer = 42;`;

test("json module type is allowed for module import destination", () => {
  assert.equal(isModuleTypeAllowed("json", "module"), true);
});

test("json module type is rejected for classic script destination", () => {
  assert.equal(isModuleTypeAllowed("json", "script"), false);
});

test("empty module type is allowed for both destinations", () => {
  assert.equal(isModuleTypeAllowed("", "module"), true);
  assert.equal(isModuleTypeAllowed("", "script"), true);
});

test("same json url loads via module import but fails as classic script", () => {
  const viaImport = loadModule(
    { url: jsonUrl, destination: "module" },
    jsonSource,
  );
  assert.equal(viaImport.moduleType, "json");
  assert.equal(viaImport.source, jsonSource);

  assert.throws(
    () => loadModule({ url: jsonUrl, destination: "script" }, jsonSource),
    /module type "json" is not allowed for destination "script"/,
  );
});

test("empty-type module script still executes as classic script", () => {
  const viaScript = loadModule(
    { url: jsUrl, destination: "script" },
    jsSource,
  );
  assert.equal(viaScript.moduleType, "");
  assert.equal(viaScript.source, jsSource);
});
