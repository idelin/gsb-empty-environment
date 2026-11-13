import assert from "node:assert/strict";
import test from "node:test";

import {
  createSettingsMerger,
  mergeSettings,
  mergeWorkspaceSettings,
  type Settings,
} from "./index.ts";

function snapshot(value: Settings): string {
  return JSON.stringify(value);
}

test("untrusted workspace and folder interpreter paths are ignored", () => {
  const user = {
    "python.defaultInterpreterPath": "/usr/bin/python3",
    "editor.fontSize": 12,
  };
  const workspace = {
    "python.defaultInterpreterPath": "./bin/python",
    "editor.fontSize": 14,
  };
  const folder = {
    "python.defaultInterpreterPath": "./tools/python",
  };
  const restrictedKeys = ["python.defaultInterpreterPath"];

  const result = mergeSettings(
    user,
    workspace,
    folder,
    false,
    restrictedKeys,
  );

  assert.equal(
    result.settings["python.defaultInterpreterPath"],
    "/usr/bin/python3",
  );
  assert.equal(result.settings["editor.fontSize"], 14);
  assert.deepEqual(result.ignoredKeys, [
    "python.defaultInterpreterPath",
    "python.defaultInterpreterPath",
  ]);
  assert.deepEqual(
    result.ignored.map(({ key, layer }) => ({ key, layer })),
    [
      { key: "python.defaultInterpreterPath", layer: "workspace" },
      { key: "python.defaultInterpreterPath", layer: "folder" },
    ],
  );

  const trustedResult = mergeSettings(
    user,
    workspace,
    folder,
    true,
    restrictedKeys,
  );
  assert.equal(
    trustedResult.settings["python.defaultInterpreterPath"],
    "./tools/python",
  );
  assert.deepEqual(trustedResult.ignored, []);
});

test("objects containing path or args at any depth are restricted", () => {
  const user = {
    "terminal.integrated.profiles.osx": {
      safe: { path: "/bin/zsh", args: [] },
    },
  };
  const workspace = {
    "terminal.integrated.profiles.osx": {
      nested: {
        deeper: {
          command: { path: "./bin/evil-shell" },
        },
      },
    },
    "terminal.integrated.env.osx": {
      wrapper: { options: { values: { args: ["-c", "./run"] } } },
    },
  };

  const result = mergeSettings(user, workspace, {}, false, []);

  assert.deepEqual(result.settings, user);
  assert.deepEqual(result.ignoredKeys, [
    "terminal.integrated.profiles.osx",
    "terminal.integrated.env.osx",
  ]);
});

test("dot-star prefixes match namespaces case-sensitively", () => {
  const workspace = {
    "python.defaultInterpreterPath": "./bin/python",
    "python.analysis.extraPaths": ["./evil"],
    "Python.defaultInterpreterPath": "./bin/Python",
    "pythonExtra": "not-in-the-python-namespace",
  };

  const result = mergeSettings({}, workspace, {}, false, ["python.*"]);

  assert.deepEqual(result.settings, {
    "Python.defaultInterpreterPath": "./bin/Python",
    "pythonExtra": "not-in-the-python-namespace",
  });
  assert.deepEqual(result.ignoredKeys, [
    "python.defaultInterpreterPath",
    "python.analysis.extraPaths",
  ]);
});

test("folder values override workspace values when unrestricted", () => {
  const user = { "editor.defaultFormatter": "builtin" };
  const workspace = { "editor.defaultFormatter": "./workspace-formatter" };
  const folder = { "editor.defaultFormatter": "./folder-formatter" };
  const result = mergeSettings(
    user,
    workspace,
    folder,
    false,
    ["editor.defaultFormatter"],
  );

  assert.deepEqual(result.settings, { "editor.defaultFormatter": "builtin" });

  const workspaceValueRetained = mergeSettings(
    user,
    { "editor.defaultFormatter": "./workspace-formatter" },
    { "editor.defaultFormatter": "./folder-formatter" },
    false,
    [],
  );
  assert.equal(
    workspaceValueRetained.settings["editor.defaultFormatter"],
    "./folder-formatter",
  );

  const folderRestricted = mergeSettings(
    user,
    { "editor.defaultFormatter": "./workspace-formatter" },
    folder,
    false,
    ["editor.defaultFormatter"],
  );
  assert.equal(
    folderRestricted.settings["editor.defaultFormatter"],
    "./workspace-formatter",
  );
});

test("trust changes are recomputed instead of reusing a prior result", () => {
  const merger = createSettingsMerger({
    userSettings: { "python.defaultInterpreterPath": "/usr/bin/python3" },
    workspaceSettings: { "python.defaultInterpreterPath": "./bin/python" },
    folderSettings: { "python.defaultInterpreterPath": "./tools/python" },
    restrictedKeys: ["python.defaultInterpreterPath"],
  });

  const untrusted = merger.merge();
  assert.equal(
    untrusted.settings["python.defaultInterpreterPath"],
    "/usr/bin/python3",
  );
  assert.equal(untrusted.ignored.length, 2);

  merger.setTrusted(true);
  const trusted = merger.merge();
  assert.equal(
    trusted.settings["python.defaultInterpreterPath"],
    "./tools/python",
  );
  assert.deepEqual(trusted.ignored, []);

  merger.setTrusted(false);
  const untrustedAgain = merger.merge();
  assert.equal(
    untrustedAgain.settings["python.defaultInterpreterPath"],
    "/usr/bin/python3",
  );
  assert.equal(untrustedAgain.ignored.length, 2);
  assert.notEqual(untrusted, trusted);
  assert.notEqual(trusted, untrustedAgain);
});

test("nested exact keys retain the lower layer value", () => {
  const user = {
    python: {
      defaultInterpreterPath: "/usr/bin/python3",
      analysis: { extraPaths: ["./safe"] },
    },
  };
  const workspace = {
    python: {
      defaultInterpreterPath: "./bin/python",
      analysis: { extraPaths: ["./evil"] },
    },
  };

  const result = mergeSettings(user, workspace, {}, false, [
    "python.defaultInterpreterPath",
  ]);

  assert.deepEqual(result.settings, {
    python: {
      defaultInterpreterPath: "/usr/bin/python3",
      analysis: { extraPaths: ["./evil"] },
    },
  });
  assert.deepEqual(result.ignoredKeys, [
    "python.defaultInterpreterPath",
  ]);
});

test("user-layer restricted values always apply and inputs are not mutated", () => {
  const user = {
    interpreter: "./user-interpreter",
    "terminal.integrated.profiles.osx": {
      safe: { path: "/bin/zsh" },
    },
  };
  const workspace = {
    interpreter: "./workspace-interpreter",
    "terminal.integrated.profiles.osx": {
      evil: { path: "./evil-shell" },
    },
  };
  const folder = { interpreter: "./folder-interpreter" };
  const before = [snapshot(user), snapshot(workspace), snapshot(folder)];

  const result = mergeSettings(
    user,
    workspace,
    folder,
    false,
    ["interpreter"],
  );

  assert.deepEqual(result.settings, user);
  assert.deepEqual(
    [snapshot(user), snapshot(workspace), snapshot(folder)],
    before,
  );
});

test("workspace-settings option aliases use the same merge rules", () => {
  const result = mergeWorkspaceSettings({
    user: { "git.path": "/usr/bin/git" },
    workspace: { "git.path": "./bin/git" },
    trust: false,
    restricted: ["git.path"],
  });

  assert.equal(result.settings["git.path"], "/usr/bin/git");
  assert.equal(result.merged["git.path"], "/usr/bin/git");
  assert.deepEqual(result.ignoredSettings, result.ignored);
});

test("positional calls are not mistaken for option objects", () => {
  const user = {
    trusted: "a normal user setting",
    workspace: "another normal user setting",
    "editor.fontSize": 11,
  };

  const result = mergeSettings(
    user,
    { "editor.fontSize": 13 },
    {},
    false,
    [],
  );

  assert.equal(result.settings.trusted, "a normal user setting");
  assert.equal(result.settings.workspace, "another normal user setting");
  assert.equal(result.settings["editor.fontSize"], 13);
});
