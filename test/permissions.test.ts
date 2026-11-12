import assert from "node:assert/strict";
import test from "node:test";

import {
  CrossOriginHandleError,
  FileSystemAccessHost,
  InvalidEntryNameError,
  type PermissionedDirectoryHandle,
  type PermissionRequest,
} from "../index.ts";

test("derived handles never exceed their parent's permission", async () => {
  const host = new FileSystemAccessHost({
    origin: "https://app.example",
  });
  const root = host.createRootDirectory("project", { mode: "read" });
  const child = await root.getDirectoryHandle("notes");
  const file = await child.getFileHandle("today.txt");

  assert.equal(await root.queryPermission(), "granted");
  assert.equal(await root.queryPermission({ mode: "readwrite" }), "prompt");
  assert.equal(await child.queryPermission(), "granted");
  assert.equal(await child.queryPermission({ mode: "readwrite" }), "prompt");
  assert.equal(await file.queryPermission(), "granted");
  assert.equal(await file.queryPermission({ mode: "readwrite" }), "prompt");
});

test("revoking a parent makes existing descendants prompt", async () => {
  const host = new FileSystemAccessHost({
    origin: "https://app.example",
  });
  const root = host.createRootDirectory("project", { mode: "readwrite" });
  const child = await root.getDirectoryHandle("notes");
  const file = await child.getFileHandle("today.txt");

  assert.equal(await file.queryPermission({ mode: "readwrite" }), "granted");
  assert.equal(host.revokePermission(root, "read"), "prompt");

  assert.equal(await root.queryPermission(), "prompt");
  assert.equal(await child.queryPermission(), "prompt");
  assert.equal(await child.queryPermission({ mode: "readwrite" }), "prompt");
  assert.equal(await file.queryPermission(), "prompt");
  assert.equal(await file.queryPermission({ mode: "readwrite" }), "prompt");
});

test("a write-only revocation preserves read but invalidates descendant writes", async () => {
  const host = new FileSystemAccessHost({
    origin: "https://app.example",
  });
  const root = host.createRootDirectory("project", { mode: "readwrite" });
  const child = await root.getDirectoryHandle("notes");
  const file = await child.getFileHandle("today.txt");

  host.revokePermission(root, "readwrite");
  assert.equal(await child.queryPermission(), "granted");
  assert.equal(await child.queryPermission({ mode: "readwrite" }), "prompt");
  assert.equal(await file.queryPermission(), "granted");
  assert.equal(await file.queryPermission({ mode: "readwrite" }), "prompt");
});

test("child names reject path separators and parent traversal", async () => {
  const host = new FileSystemAccessHost("https://app.example");
  const root = host.createRootDirectory("project");

  for (const name of [
    "..",
    "../escape",
    "a/../b",
    "a\\..\\b",
    "a/b",
    "a\\b",
    "file..txt",
  ]) {
    await assert.rejects(
      root.getFileHandle(name),
      (error: unknown) =>
        error instanceof InvalidEntryNameError &&
        error.code === "InvalidEntryNameError",
      `getFileHandle should reject ${JSON.stringify(name)}`,
    );
  }

  await assert.rejects(root.getDirectoryHandle(".."), InvalidEntryNameError);
  assert.throws(
    () => host.createRootDirectory("../project"),
    InvalidEntryNameError,
  );
});

test("a handle cannot be cloned to another origin", async () => {
  const host = new FileSystemAccessHost("https://app.example");
  const otherHost = new FileSystemAccessHost("https://evil.example");
  const root = host.createRootDirectory("project", { mode: "readwrite" });

  assert.throws(
    () => root.cloneForOrigin("https://evil.example"),
    CrossOriginHandleError,
  );
  assert.throws(() => root.cloneForHost(otherHost), CrossOriginHandleError);

  const sameOrigin = root.cloneForOrigin("https://app.example/");
  assert.equal(sameOrigin.id, root.id);
  assert.equal(
    await sameOrigin.queryPermission({ mode: "readwrite" }),
    "granted",
  );
});

test("restored grants keep read but require confirmation for write", async () => {
  const host = new FileSystemAccessHost("https://app.example");
  const root = host.createRootDirectory("project", { mode: "readwrite" });
  const child = await root.getDirectoryHandle("notes");
  const file = await child.getFileHandle("today.txt");
  const snapshot = host.persist();

  assert.equal(
    snapshot.handles.find((entry) => entry.id === file.id)?.readGranted,
    true,
  );
  assert.equal("writeGranted" in snapshot.handles[0]!, false);

  const requests: PermissionRequest[] = [];
  const restored = FileSystemAccessHost.restore(snapshot, {
    confirmPermission: (request) => {
      requests.push(request);
      return true;
    },
  });

  const restoredRoot = restored.getHandle(root.id);
  const restoredChild = restored.getHandle(child.id);
  const restoredFile = restored.getHandle(file.id);
  assert.equal(restoredRoot?.kind, "directory");
  assert.equal(restoredChild?.kind, "directory");
  assert.equal(restoredFile?.kind, "file");

  assert.equal(await restoredRoot!.queryPermission(), "granted");
  assert.equal(
    await restoredRoot!.queryPermission({ mode: "readwrite" }),
    "prompt",
  );
  assert.equal(await restoredFile!.queryPermission(), "granted");
  assert.equal(
    await restoredFile!.queryPermission({ mode: "readwrite" }),
    "prompt",
  );

  assert.equal(
    await restoredRoot!.requestPermission({ mode: "readwrite" }),
    "granted",
  );
  assert.equal(
    await restoredChild!.requestPermission({ mode: "readwrite" }),
    "granted",
  );
  assert.equal(
    await restoredFile!.requestPermission({ mode: "readwrite" }),
    "granted",
  );
  assert.equal(requests.length, 3);
  assert.equal(requests[0]?.mode, "readwrite");
  assert.equal(requests[0]?.afterRestore, true);
  assert.equal(
    await restoredFile!.queryPermission({ mode: "readwrite" }),
    "granted",
  );
});

test("restore rejects a different origin and unsafe persisted names", async () => {
  const host = new FileSystemAccessHost("https://app.example");
  const root = host.createRootDirectory("project", { mode: "readwrite" });
  const snapshot = host.persist();

  assert.throws(
    () =>
      FileSystemAccessHost.restore(snapshot, {
        origin: "https://evil.example",
      }),
    CrossOriginHandleError,
  );

  const unsafe = {
    ...snapshot,
    handles: snapshot.handles.map((entry) =>
      entry.id === root.id ? { ...entry, name: ".." } : entry,
    ),
  };
  assert.throws(() => FileSystemAccessHost.restore(unsafe), InvalidEntryNameError);
});

test("revoked descendant state stays prompt after restore", async () => {
  const host = new FileSystemAccessHost({
    origin: "https://app.example",
    confirmPermission: () => true,
  });
  const root = host.createRootDirectory("project", { mode: "readwrite" });
  const child = await root.getDirectoryHandle("notes");
  host.revokePermission(root, { mode: "readwrite" });

  const restored = FileSystemAccessHost.restore(host.persist(), {
    confirmPermission: () => true,
  });
  const restoredRoot = restored.getHandle(root.id);
  const restoredChild = restored.getHandle(child.id);

  assert.equal(await restoredChild!.queryPermission(), "granted");
  assert.equal(
    await restoredChild!.queryPermission({ mode: "readwrite" }),
    "prompt",
  );

  assert.equal(
    await restoredRoot!.requestPermission({ mode: "readwrite" }),
    "granted",
  );
  assert.equal(
    await restoredChild!.queryPermission({ mode: "readwrite" }),
    "prompt",
  );
});

test("write confirmation cannot raise permissions above the parent", async () => {
  const host = new FileSystemAccessHost({
    origin: "https://app.example",
    confirmPermission: () => true,
  });
  const root = host.createRootDirectory("project", { mode: "read" });
  const child = await root.getDirectoryHandle("notes");

  assert.equal(
    await child.requestPermission({ mode: "readwrite" }),
    "prompt",
  );
  assert.equal(await child.queryPermission(), "granted");
  assert.equal(
    await child.queryPermission({ mode: "readwrite" }),
    "prompt",
  );
});

test("stale approval cannot override a revocation during confirmation", async () => {
  let host!: FileSystemAccessHost;
  let root!: PermissionedDirectoryHandle;

  host = new FileSystemAccessHost({
    origin: "https://app.example",
    confirmPermission: () => {
      host.revokePermission(root, "read");
      return true;
    },
  });
  root = host.createRootDirectory("project", { mode: "prompt" });

  assert.equal(await root.requestPermission(), "prompt");
  assert.equal(await root.queryPermission(), "prompt");
});

test("revoked descendants stay prompt after a parent is re-granted", async () => {
  const host = new FileSystemAccessHost({
    origin: "https://app.example",
    confirmPermission: () => true,
  });
  const root = host.createRootDirectory("project", { mode: "readwrite" });
  const oldChild = await root.getDirectoryHandle("old");

  host.revokePermission(root, "read");
  assert.equal(
    await root.requestPermission({ mode: "readwrite" }),
    "granted",
  );

  assert.equal(await oldChild.queryPermission(), "prompt");
  assert.equal(
    await oldChild.queryPermission({ mode: "readwrite" }),
    "prompt",
  );

  const newChild = await root.getDirectoryHandle("new");
  assert.equal(await newChild.queryPermission(), "granted");
  assert.equal(
    await newChild.queryPermission({ mode: "readwrite" }),
    "granted",
  );
});
