import assert from "node:assert/strict";
import {
  chmodSync,
  mkdirSync,
  mkdtempSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { after, before, describe, it } from "node:test";
import { checkPermission, decidePermission } from "./index.ts";

let root: string;
let base: string;
let base2: string;
let secret: string;
let outside: string;

before(() => {
  root = mkdtempSync(path.join(tmpdir(), "perm-check-"));
  base = path.join(root, "base");
  base2 = path.join(root, "base2");
  secret = path.join(base, "secret");
  outside = path.join(root, "outside");
  mkdirSync(base, { recursive: true });
  mkdirSync(base2, { recursive: true });
  mkdirSync(secret, { recursive: true });
  mkdirSync(outside, { recursive: true });
  writeFileSync(path.join(base, "ok.txt"), "ok");
  writeFileSync(path.join(secret, "key.txt"), "key");
  writeFileSync(path.join(outside, "loot.txt"), "loot");
  symlinkSync(secret, path.join(base, "link-to-secret"), "dir");
  symlinkSync(outside, path.join(base, "link-to-outside"), "dir");
});

after(() => {
  rmSync(root, { recursive: true, force: true });
});

describe("path permissions", () => {
  it("covers children but not same-prefix siblings", () => {
    const lists = { allow: { read: [base] } };
    assert.equal(
      checkPermission(lists, { kind: "read", path: path.join(base, "ok.txt") }),
      true,
    );
    assert.equal(
      checkPermission(lists, {
        kind: "read",
        path: path.join(base2, "file.txt"),
      }),
      false,
    );
  });

  it("rejects .. escaping the granted directory", () => {
    const lists = { allow: { read: [base] } };
    assert.equal(
      checkPermission(lists, {
        kind: "read",
        path: path.join(base, "..", "outside", "loot.txt"),
      }),
      false,
    );
  });

  it("rejects symlinks resolving into a denied directory", () => {
    const lists = { allow: { read: [base] }, deny: { read: [secret] } };
    assert.equal(
      checkPermission(lists, {
        kind: "read",
        path: path.join(base, "link-to-secret", "key.txt"),
      }),
      false,
    );
  });

  it("rejects symlinks escaping the allowed tree entirely", () => {
    const lists = { allow: { read: [base] } };
    assert.equal(
      checkPermission(lists, {
        kind: "read",
        path: path.join(base, "link-to-outside", "loot.txt"),
      }),
      false,
    );
  });

  it("deny wins over allow for direct paths", () => {
    const lists = { allow: { read: [base] }, deny: { read: [secret] } };
    assert.equal(
      checkPermission(lists, {
        kind: "read",
        path: path.join(secret, "key.txt"),
      }),
      false,
    );
    assert.equal(
      checkPermission(lists, { kind: "read", path: path.join(base, "ok.txt") }),
      true,
    );
  });

  it("a directory containing a deny entry is not fully authorized", () => {
    const lists = { allow: { read: [base] }, deny: { read: [secret] } };
    const decision = decidePermission(lists, { kind: "read", path: base });
    assert.equal(decision.granted, false);
    assert.match(decision.reason, /deny entry exists inside/);
    const clean = { allow: { read: [base2] } };
    assert.equal(
      checkPermission(clean, { kind: "read", path: base2 }),
      true,
    );
  });

  it("applies the same rules to write", () => {
    const lists = { allow: { write: [base] }, deny: { write: [secret] } };
    assert.equal(
      checkPermission(lists, {
        kind: "write",
        path: path.join(base, "new.txt"),
      }),
      true,
    );
    assert.equal(
      checkPermission(lists, {
        kind: "write",
        path: path.join(secret, "new.txt"),
      }),
      false,
    );
  });
});

describe("net permissions", () => {
  const lists = {
    allow: { net: ["example.com", "[::1]:8080"] },
    deny: { net: ["example.com:8080"] },
  };

  it("host-only entries allow every port", () => {
    assert.equal(
      checkPermission(lists, { kind: "net", host: "example.com", port: 443 }),
      true,
    );
  });

  it("host:port entries allow only that port", () => {
    assert.equal(
      checkPermission(lists, { kind: "net", host: "::1", port: 8080 }),
      true,
    );
    assert.equal(
      checkPermission(lists, { kind: "net", host: "[::1]", port: 8080 }),
      true,
    );
    assert.equal(
      checkPermission(lists, { kind: "net", host: "::1", port: 9090 }),
      false,
    );
  });

  it("deny entries beat broader allow entries", () => {
    assert.equal(
      checkPermission(lists, { kind: "net", host: "example.com", port: 8080 }),
      false,
    );
  });

  it("rejects unlisted hosts", () => {
    assert.equal(
      checkPermission(lists, { kind: "net", host: "evil.com", port: 443 }),
      false,
    );
  });
});

describe("env permissions", () => {
  const lists = {
    allow: { env: ["AWS_*", "HOME"] },
    deny: { env: ["AWS_SECRET_*"] },
  };

  it("wildcard entries match by prefix", () => {
    assert.equal(
      checkPermission(lists, { kind: "env", name: "AWS_REGION" }),
      true,
    );
    assert.equal(checkPermission(lists, { kind: "env", name: "HOME" }), true);
    assert.equal(checkPermission(lists, { kind: "env", name: "PATH" }), false);
  });

  it("deny wildcards beat allow wildcards", () => {
    assert.equal(
      checkPermission(lists, { kind: "env", name: "AWS_SECRET_KEY" }),
      false,
    );
  });
});

describe("run permissions", () => {
  let binDir: string;
  let evilDir: string;
  let realPath: string;
  let evilPath: string;
  let savedPath: string | undefined;

  before(() => {
    binDir = path.join(root, "bin");
    evilDir = path.join(root, "evil-bin");
    mkdirSync(binDir, { recursive: true });
    mkdirSync(evilDir, { recursive: true });
    realPath = path.join(binDir, "deploy");
    evilPath = path.join(evilDir, "deploy");
    writeFileSync(realPath, "#!/bin/sh\nexit 0\n");
    writeFileSync(evilPath, "#!/bin/sh\nexit 1\n");
    chmodSync(realPath, 0o755);
    chmodSync(evilPath, 0o755);
    savedPath = process.env.PATH;
    process.env.PATH = `${binDir}${path.delimiter}${savedPath ?? ""}`;
  });

  after(() => {
    process.env.PATH = savedPath;
  });

  it("allows a command granted by name", () => {
    const lists = { allow: { run: ["deploy"] } };
    assert.equal(checkPermission(lists, { kind: "run", command: "deploy" }), true);
  });

  it("allows the path form when it resolves to the granted name", () => {
    const lists = { allow: { run: ["deploy"] } };
    assert.equal(
      checkPermission(lists, { kind: "run", command: realPath }),
      true,
    );
  });

  it("rejects a same-named program resolving elsewhere", () => {
    const lists = { allow: { run: ["deploy"] } };
    assert.equal(
      checkPermission(lists, { kind: "run", command: evilPath }),
      false,
    );
  });

  it("grants by absolute path and rejects others", () => {
    const lists = { allow: { run: [realPath] } };
    assert.equal(
      checkPermission(lists, { kind: "run", command: realPath }),
      true,
    );
    assert.equal(
      checkPermission(lists, { kind: "run", command: "deploy" }),
      true,
    );
    assert.equal(
      checkPermission(lists, { kind: "run", command: evilPath }),
      false,
    );
  });

  it("deny wins over allow for run", () => {
    const lists = {
      allow: { run: ["deploy"] },
      deny: { run: [realPath] },
    };
    assert.equal(
      checkPermission(lists, { kind: "run", command: "deploy" }),
      false,
    );
  });
});
