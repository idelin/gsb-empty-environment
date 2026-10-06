import { strict as assert } from "assert";
import { Namespace } from "../src/namespace";
import {
  MoveValidationError,
  NotFoundError,
  ReadOnlyError,
  StaleConfigError,
} from "../src/errors";
import { test } from "./harness";

function makeNamespace(): Namespace {
  const ns = new Namespace();
  ns.mounts.addMount("main", ns.tree.rootId, false);
  return ns;
}

test("committed move updates real paths while aliases keep target ids", () => {
  const ns = makeNamespace();
  const tree = ns.tree;
  const src = tree.createNode("src", tree.rootId);
  const pkg = tree.createNode("pkg", src);
  const dest = tree.createNode("dest", tree.rootId);
  ns.aliases.add("/pkg", pkg);

  ns.beginMove("/src", "/dest").prepare().commit();

  assert.equal(tree.realPath(src), "/dest/src");
  assert.equal(tree.realPath(pkg), "/dest/src/pkg");
  assert.equal(ns.aliases.targetOf("/pkg"), pkg);
  assert.equal(ns.resolvePath("/pkg").realPath, "/dest/src/pkg");
});

test("moving a node into its own subtree fails validation and changes nothing", () => {
  const ns = makeNamespace();
  const tree = ns.tree;
  const a = tree.createNode("a", tree.rootId);
  const b = tree.createNode("b", a);

  assert.throws(() => ns.beginMove("/a", "/a/b").prepare(), MoveValidationError);
  assert.equal(tree.realPath(a), "/a");
  assert.equal(tree.realPath(b), "/a/b");
});

test("name conflicts at the target fail validation and change nothing", () => {
  const ns = makeNamespace();
  const tree = ns.tree;
  const src = tree.createNode("item", tree.rootId);
  const dest = tree.createNode("dest", tree.rootId);
  tree.createNode("item", dest);

  assert.throws(() => ns.beginMove("/item", "/dest").prepare(), MoveValidationError);
  assert.equal(tree.realPath(src), "/item");
});

test("read-only mounts reject moves sourced from or targeted at them", () => {
  const ns = makeNamespace();
  const tree = ns.tree;
  const roRoot = tree.createNode("ro-root", tree.rootId);
  tree.createNode("locked", roRoot);
  const dest = tree.createNode("dest", tree.rootId);
  ns.mounts.addMount("ro", roRoot, true);
  ns.mounts.attach("ro", "main", "/ro");

  assert.throws(() => ns.beginMove("/ro/locked", "/dest").prepare(), ReadOnlyError);
  assert.throws(() => ns.beginMove("/dest", "/ro").prepare(), ReadOnlyError);
  assert.equal(tree.realPath(roRoot), "/ro-root");
  assert.equal(tree.realPath(dest), "/dest");
});

test("configuration switch after validation aborts the commit without changes", () => {
  const ns = makeNamespace();
  const tree = ns.tree;
  const src = tree.createNode("src", tree.rootId);
  tree.createNode("dest", tree.rootId);
  const extra = tree.createNode("extra", tree.rootId);

  const tx = ns.beginMove("/src", "/dest").prepare();
  ns.aliases.add("/late-alias", extra);

  assert.throws(() => tx.commit(), StaleConfigError);
  assert.equal(tree.realPath(src), "/src");
});

test("a source deleted between prepare and commit fails the commit safely", () => {
  const ns = makeNamespace();
  const tree = ns.tree;
  const src = tree.createNode("src", tree.rootId);
  tree.createNode("dest", tree.rootId);

  const tx = ns.beginMove("/src", "/dest").prepare();
  tree.deleteNode(src);

  assert.throws(() => tx.commit(), NotFoundError);
  assert.equal(tree.idAtPath("/dest/src"), null);
});
