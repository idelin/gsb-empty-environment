import { strict as assert } from "assert";
import { Namespace } from "../src/namespace";
import { ReadOnlyError, RootEscapeError } from "../src/errors";
import { test } from "./harness";

interface Fixture {
  ns: Namespace;
  dataId: string;
  fileId: string;
  assetsRootId: string;
  imgId: string;
}

function makeFixture(): Fixture {
  const ns = new Namespace();
  const tree = ns.tree;
  const dataId = tree.createNode("data", tree.rootId);
  const fileId = tree.createNode("file.txt", dataId);
  const assetsRootId = tree.createNode("assets-root", tree.rootId);
  const imgId = tree.createNode("img.png", assetsRootId);

  ns.mounts.addMount("main", tree.rootId, false);
  ns.mounts.addMount("assets", assetsRootId, true);
  ns.mounts.attach("assets", "main", "/data/assets");
  return { ns, dataId, fileId, assetsRootId, imgId };
}

test("paths resolve across mount boundaries to the owning mount", () => {
  const { ns, fileId, imgId } = makeFixture();
  const plain = ns.resolvePath("/data/file.txt");
  assert.equal(plain.nodeId, fileId);
  assert.equal(plain.mount.name, "main");

  const mounted = ns.resolvePath("/data/assets/img.png");
  assert.equal(mounted.nodeId, imgId);
  assert.equal(mounted.mount.name, "assets");
});

test("aliases cannot smuggle writes across a read-only mount boundary", () => {
  const { ns, imgId } = makeFixture();
  ns.aliases.add("/pic", imgId);

  assert.equal(ns.resolvePath("/pic").nodeId, imgId);
  assert.throws(() => ns.resolvePath("/pic", { forWrite: true }), ReadOnlyError);
  assert.throws(
    () => ns.resolvePath("/data/assets/img.png", { forWrite: true }),
    ReadOnlyError,
  );
  assert.doesNotThrow(() => ns.resolvePath("/data/file.txt", { forWrite: true }));
});

test("'..' cannot escape above a mount root", () => {
  const { ns } = makeFixture();
  assert.throws(() => ns.resolvePath("/.."), RootEscapeError);
  assert.throws(() => ns.resolvePath("/data/../.."), RootEscapeError);
  assert.throws(() => ns.resolvePath("/data/assets/.."), RootEscapeError);
  assert.equal(ns.resolvePath("/data/../data/file.txt").mount.name, "main");
});

test("cyclic mount graphs resolve finite paths and still block root escape", () => {
  const { ns, imgId } = makeFixture();
  ns.mounts.attach("main", "assets", "/back");

  const looped = ns.resolvePath("/data/assets/back/data/assets/img.png");
  assert.equal(looped.nodeId, imgId);
  assert.equal(looped.mount.name, "assets");

  assert.throws(
    () => ns.resolvePath("/data/assets/back/data/assets/.."),
    RootEscapeError,
  );
});

test("alias and mount configuration changes bump the config version", () => {
  const { ns, imgId } = makeFixture();
  const v0 = ns.configVersion;
  ns.aliases.add("/pic", imgId);
  const v1 = ns.configVersion;
  assert.ok(v1 > v0);
  ns.aliases.remove("/pic");
  assert.ok(ns.configVersion > v1);
  ns.mounts.addMount("extra", ns.tree.createNode("extra", ns.tree.rootId));
  assert.ok(ns.configVersion > v1 + 1);
});
