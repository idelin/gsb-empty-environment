import { strict as assert } from "assert";
import { AliasTable } from "../src/alias";
import {
  AliasConflictError,
  CycleError,
  DeletedTargetError,
} from "../src/errors";
import { Tree } from "../src/tree";
import { test } from "./harness";

function makeTree(): { tree: Tree; aliases: AliasTable } {
  const tree = new Tree();
  return { tree, aliases: new AliasTable(tree) };
}

test("longest alias prefix wins over shorter prefixes", () => {
  const { tree, aliases } = makeTree();
  const libs = tree.createNode("libs", tree.rootId);
  tree.createNode("a", libs);
  const special = tree.createNode("special-target", tree.rootId);
  tree.createNode("deep", special);

  aliases.add("/libs", libs);
  aliases.add("/libs/special", special);

  assert.equal(aliases.resolve("/libs/a").path, "/libs/a");
  assert.equal(aliases.resolve("/libs/special").path, "/special-target");
  assert.equal(aliases.resolve("/libs/special/deep").path, "/special-target/deep");
});

test("nested aliases chain through intermediate real paths", () => {
  const { tree, aliases } = makeTree();
  const actual = tree.createNode("actual", tree.rootId);
  const deep = tree.createNode("deep", actual);
  const sub = tree.createNode("sub", deep);
  const leaf = tree.createNode("leaf", sub);
  const rDir = tree.createNode("r", tree.rootId);
  const rSub = tree.createNode("sub", rDir);
  const qDir = tree.createNode("q", tree.rootId);
  const qLeaf = tree.createNode("leaf", qDir);

  aliases.add("/r", deep);
  aliases.add("/q", rSub);
  aliases.add("/p", qLeaf);

  const resolution = aliases.resolve("/p");
  assert.equal(resolution.path, "/actual/deep/sub/leaf");
  assert.equal(resolution.hops, 3);
  assert.deepEqual(resolution.chain, ["/p", "/q", "/r"]);
  assert.equal(aliases.resolveId("/p").id, leaf);
});

test("bounded-hop interpreter aborts when the hop budget is exceeded", () => {
  const tree = new Tree();
  const aliases = new AliasTable(tree, 2);
  const actual = tree.createNode("actual", tree.rootId);
  const deep = tree.createNode("deep", actual);
  const rDir = tree.createNode("r", tree.rootId);
  const rSub = tree.createNode("sub", rDir);
  const qDir = tree.createNode("q", tree.rootId);
  const qLeaf = tree.createNode("leaf", qDir);

  aliases.add("/r", deep);
  aliases.add("/q", rSub);

  // Adding a third link is rejected: validating it would need 3 hops.
  assert.throws(() => aliases.add("/p", qLeaf), CycleError);
  assert.equal(aliases.has("/p"), false);

  // Existing chains within the budget still resolve.
  assert.equal(aliases.resolve("/q").path, "/actual/deep/sub");
});

test("a resolution loop created by a later move is caught at resolve time", () => {
  const tree = new Tree();
  const aliases = new AliasTable(tree, 8);
  const actual = tree.createNode("actual", tree.rootId);
  const deep = tree.createNode("deep", actual);
  const rDir = tree.createNode("r", tree.rootId);

  aliases.add("/r", deep);
  assert.equal(aliases.resolve("/r").path, "/actual/deep");

  // Moving the target under the alias's own subtree creates a loop; the
  // bounded-hop interpreter aborts instead of spinning forever.
  tree.moveNode(actual, rDir);
  assert.throws(() => aliases.resolve("/r"), CycleError);
});

test("adding an alias that would form a resolution cycle is rejected and rolled back", () => {
  const { tree, aliases } = makeTree();
  const aDir = tree.createNode("a", tree.rootId);
  const aX = tree.createNode("x", aDir);
  const bDir = tree.createNode("b", tree.rootId);
  const bY = tree.createNode("y", bDir);

  aliases.add("/a", bY);
  assert.throws(() => aliases.add("/b", aX), CycleError);
  assert.equal(aliases.has("/b"), false);
  assert.equal(aliases.resolve("/a").path, "/b/y");
});

test("moving a node updates its real path but keeps alias target ids", () => {
  const { tree, aliases } = makeTree();
  const libs = tree.createNode("libs", tree.rootId);
  tree.createNode("util", libs);
  const elsewhere = tree.createNode("elsewhere", tree.rootId);

  aliases.add("/libs", libs);
  const targetBefore = aliases.targetOf("/libs");
  assert.equal(aliases.resolve("/libs/util").path, "/libs/util");

  tree.moveNode(libs, elsewhere);
  assert.equal(tree.realPath(libs), "/elsewhere/libs");
  assert.equal(aliases.targetOf("/libs"), targetBefore);
  assert.equal(aliases.resolve("/libs/util").path, "/elsewhere/libs/util");
});

test("resolving an alias whose target was deleted raises DeletedTargetError", () => {
  const { tree, aliases } = makeTree();
  const libs = tree.createNode("libs", tree.rootId);
  aliases.add("/libs", libs);

  tree.deleteNode(libs);
  assert.throws(() => aliases.resolve("/libs"), DeletedTargetError);
  assert.throws(() => aliases.resolve("/libs/anything"), DeletedTargetError);
});

test("same-prefix alias conflicts are rejected; distinct nested prefixes coexist", () => {
  const { tree, aliases } = makeTree();
  const a = tree.createNode("a", tree.rootId);
  const b = tree.createNode("b", tree.rootId);

  aliases.add("/x/y", a);
  assert.throws(() => aliases.add("/x/y", b), AliasConflictError);
  assert.throws(() => aliases.add("x/y/", b), AliasConflictError);
  assert.equal(aliases.targetOf("/x/y"), a);

  aliases.add("/x", b);
  assert.equal(aliases.targetOf("/x"), b);
  assert.equal(aliases.resolve("/x/y").path, "/a");
});
