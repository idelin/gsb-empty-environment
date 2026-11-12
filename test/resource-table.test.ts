import assert from "node:assert/strict";
import test from "node:test";
import {
  HandleError,
  ResourceTable,
  WorldAuthorizationError,
  instantiateComponent,
} from "../index.ts";
import type { ResourceHandle } from "../index.ts";

function hasCode(code: HandleError["code"]) {
  return (error: unknown): boolean =>
    error instanceof HandleError && error.code === code;
}

test("type confusion: an A-type handle cannot be used as B", () => {
  const table = new ResourceTable();
  const file = table.createOwn("file", { descriptor: 7 });

  assert.throws(
    () => table.use(file, "socket"),
    hasCode("HANDLE_TYPE_MISMATCH"),
  );
  assert.throws(
    () => table.useOwn(file, "socket"),
    hasCode("HANDLE_TYPE_MISMATCH"),
  );
  assert.throws(
    () => table.drop(file, "socket"),
    hasCode("HANDLE_TYPE_MISMATCH"),
  );
  assert.throws(
    () => table.borrow(file, "socket", table.beginCall()),
    hasCode("HANDLE_TYPE_MISMATCH"),
  );

  assert.deepEqual(table.use(file, "file"), { descriptor: 7 });
  assert.equal(table.activeSlotCount, 1);
});

test("type confusion: a forged unissued handle is rejected", () => {
  const table = new ResourceTable();
  const issued = table.createOwn("file", "contents");

  assert.throws(
    () => table.use({ index: 0, generation: 0 } as ResourceHandle, "file"),
    hasCode("HANDLE_INVALID"),
  );
  assert.throws(
    () =>
      table.use(
        { ...issued, index: 1, generation: 99 } as ResourceHandle,
        "file",
      ),
    hasCode("HANDLE_INVALID"),
  );
  assert.throws(
    () =>
      table.use(
        Object.create(Object.getPrototypeOf(issued)) as ResourceHandle,
        "file",
      ),
    hasCode("HANDLE_INVALID"),
  );
});

test("borrow escapes are revoked when the call returns", () => {
  const table = new ResourceTable();
  const own = table.createOwn("file", "read-only");

  const scope = table.beginCall();
  const borrowed = table.borrow(own, "file", scope);
  assert.equal(table.useBorrow(borrowed, "file"), "read-only");
  assert.throws(
    () => table.useBorrow(own, "file"),
    hasCode("HANDLE_KIND_MISMATCH"),
  );
  assert.throws(
    () => table.drop(borrowed, "file"),
    hasCode("HANDLE_KIND_MISMATCH"),
  );
  assert.throws(
    () => table.transferOwn(borrowed, "file"),
    hasCode("HANDLE_KIND_MISMATCH"),
  );

  table.endCall(scope);

  assert.throws(
    () => table.use(borrowed, "file"),
    hasCode("HANDLE_BORROW_EXPIRED"),
  );
  assert.throws(
    () => table.borrow(borrowed, "file", table.beginCall()),
    hasCode("HANDLE_BORROW_EXPIRED"),
  );
  assert.equal(table.useOwn(own, "file"), "read-only");
});

test("borrow escape through an instance invoke is revoked even on throw", () => {
  const instance = instantiateComponent(
    { name: "guest", imports: ["wasi:cli/stdout@0.2.0"] },
    { name: "app", imports: ["wasi:cli/stdout@0.2.0"] },
  );
  let escaped: ResourceHandle | undefined;

  assert.throws(
    () =>
      instance.invoke((scope) => {
        const own = instance.table.createOwn("file", "secret");
        escaped = instance.table.borrow(own, "file", scope);
        throw new Error("guest failed");
      }),
    /guest failed/,
  );

  assert.ok(escaped);
  assert.throws(
    () => instance.table.use(escaped, "file"),
    hasCode("HANDLE_BORROW_EXPIRED"),
  );
});

test("async invocations revoke borrows only when the promise settles", async () => {
  const table = new ResourceTable();
  const own = table.createOwn("file", "async data");
  let escaped: ResourceHandle | undefined;

  const result = await table.withCallAsync(async (scope) => {
    escaped = table.borrow(own, "file", scope);
    await new Promise((resolve) => setTimeout(resolve, 5));
    assert.equal(table.useBorrow(escaped, "file"), "async data");
    return "done";
  });

  assert.equal(result, "done");
  assert.ok(escaped);
  assert.throws(
    () => table.use(escaped, "file"),
    hasCode("HANDLE_BORROW_EXPIRED"),
  );
});

test("nested calls only revoke borrows belonging to the returned call", () => {
  const table = new ResourceTable();
  const own = table.createOwn("file", "data");

  const outer = table.beginCall();
  const outerBorrow = table.borrow(own, "file", outer);
  const inner = table.beginCall();
  const innerBorrow = table.borrow(outerBorrow, "file", inner);

  table.endCall(inner);
  assert.equal(table.useBorrow(outerBorrow, "file"), "data");
  assert.throws(
    () => table.use(innerBorrow, "file"),
    hasCode("HANDLE_BORROW_EXPIRED"),
  );

  table.endCall(outer);
  assert.throws(
    () => table.use(outerBorrow, "file"),
    hasCode("HANDLE_BORROW_EXPIRED"),
  );
  assert.equal(table.use(own, "file"), "data");
});

test("a derived borrow pins the owning resource even if scopes end out of order", () => {
  const table = new ResourceTable();
  const own = table.createOwn("file", "pinned");

  const outer = table.beginCall();
  const outerBorrow = table.borrow(own, "file", outer);
  const inner = table.beginCall();
  const innerBorrow = table.borrow(outerBorrow, "file", inner);

  table.endCall(outer);
  assert.throws(
    () => table.use(outerBorrow, "file"),
    hasCode("HANDLE_BORROW_EXPIRED"),
  );
  assert.equal(table.useBorrow(innerBorrow, "file"), "pinned");
  assert.throws(() => table.drop(own, "file"), hasCode("HANDLE_IN_USE"));

  table.endCall(inner);
  assert.throws(
    () => table.use(innerBorrow, "file"),
    hasCode("HANDLE_BORROW_EXPIRED"),
  );
  table.drop(own, "file");
});

test("borrows cannot be created after return or while dropping the owner", () => {
  const table = new ResourceTable();
  const own = table.createOwn("file", "data");
  const scope = table.beginCall();
  table.endCall(scope);

  assert.throws(
    () => table.borrow(own, "file", scope),
    hasCode("CALL_SCOPE_CLOSED"),
  );

  const live = table.beginCall();
  const borrowed = table.borrow(own, "file", live);
  assert.throws(() => table.drop(own, "file"), hasCode("HANDLE_IN_USE"));
  assert.equal(table.useBorrow(borrowed, "file"), "data");
  table.endCall(live);
  assert.throws(
    () => table.useBorrow(borrowed, "file"),
    hasCode("HANDLE_BORROW_EXPIRED"),
  );
});

test("drop followed by slot index reuse cannot revive the stale handle", () => {
  const table = new ResourceTable();
  const first = table.createOwn("file", "first secret");
  const firstMeta = table.inspect(first);

  table.drop(first, "file");
  assert.throws(() => table.use(first, "file"), hasCode("HANDLE_DROPPED"));
  assert.throws(() => table.drop(first, "file"), hasCode("HANDLE_DROPPED"));

  const second = table.createOwn("file", "second secret");
  const secondMeta = table.inspect(second);

  assert.equal(secondMeta.index, firstMeta.index);
  assert.notEqual(secondMeta.generation, firstMeta.generation);
  assert.equal(secondMeta.type, "file");
  assert.throws(() => table.use(first, "file"), hasCode("HANDLE_STALE"));
  assert.throws(() => table.drop(first, "file"), hasCode("HANDLE_STALE"));
  assert.equal(table.use(second, "file"), "second secret");
});

test("own transfer retires the source slot", () => {
  const table = new ResourceTable();
  const source = table.createOwn("socket", { connectionId: 42 });
  const destination = table.transferOwn(source, "socket");

  assert.throws(() => table.use(source, "socket"), hasCode("HANDLE_MOVED"));
  assert.throws(() => table.drop(source, "socket"), hasCode("HANDLE_MOVED"));
  assert.deepEqual(table.useOwn(destination, "socket"), { connectionId: 42 });
});

test("own transfer across tables cannot be replayed by the source table", () => {
  const host = new ResourceTable();
  const guest = new ResourceTable();
  const source = host.createOwn("file", "host data");
  const destination = host.transferOwn(source, "file", guest);

  assert.throws(() => host.use(source, "file"), hasCode("HANDLE_MOVED"));
  assert.throws(() => guest.use(source, "file"), hasCode("HANDLE_INVALID"));
  assert.equal(guest.useOwn(destination, "file"), "host data");
});

test("empty resource types and foreign call scopes are rejected", () => {
  const table = new ResourceTable();
  assert.throws(() => table.createOwn("", "x"), TypeError);

  const other = new ResourceTable();
  const own = table.createOwn("file", "data");
  const foreignScope = other.beginCall();
  assert.throws(
    () => table.borrow(own, "file", foreignScope),
    hasCode("CALL_SCOPE_INVALID"),
  );
});

test("world subset: extra and lookalike imports are denied", () => {
  const world = {
    name: "app",
    imports: ["wasi:cli/stdout@0.2.0", "wasi:io/error@0.2.0"],
  };

  assert.throws(
    () =>
      instantiateComponent(
        {
          name: "overreaching",
          imports: ["wasi:cli/stdout@0.2.0", "wasi:cli/environment@0.2.0"],
        },
        world,
      ),
    (error: unknown) =>
      error instanceof WorldAuthorizationError &&
      error.code === "IMPORT_NOT_AUTHORIZED" &&
      error.unauthorizedImports.length === 1 &&
      error.unauthorizedImports[0] === "wasi:cli/environment@0.2.0",
  );

  assert.throws(
    () =>
      instantiateComponent(
        { imports: ["wasi:cli/stdout@0.2.0@evil"] },
        world,
      ),
    WorldAuthorizationError,
  );

  assert.throws(
    () =>
      instantiateComponent(
        { imports: ["__proto__"] },
        ["wasi:cli/stdout@0.2.0"],
      ),
    WorldAuthorizationError,
  );
});

test("world subset: every requested import must be authorized exactly", () => {
  const instance = instantiateComponent(
    {
      name: "legitimate",
      imports: ["wasi:io/error@0.2.0", "wasi:cli/stdout@0.2.0"],
    },
    new Set(["wasi:cli/stdout@0.2.0", "wasi:io/error@0.2.0", "unused"]),
  );

  assert.deepEqual(instance.imports, [
    "wasi:io/error@0.2.0",
    "wasi:cli/stdout@0.2.0",
  ]);
  assert.equal(instance.worldName, undefined);
  assert.ok(instance.table instanceof ResourceTable);
});

test("duplicate imports do not create authorization", () => {
  assert.throws(
    () =>
      instantiateComponent(
        {
          imports: [
            "wasi:cli/stdout@0.2.0",
            "wasi:fs/types@0.2.0",
            "wasi:fs/types@0.2.0",
          ],
        },
        ["wasi:cli/stdout@0.2.0"],
      ),
    (error: unknown) =>
      error instanceof WorldAuthorizationError &&
      error.unauthorizedImports.length === 1 &&
      error.unauthorizedImports[0] === "wasi:fs/types@0.2.0",
  );
});

test("malformed import collections are rejected instead of being iterated", () => {
  assert.throws(
    () =>
      instantiateComponent(
        { imports: "wasi:cli/stdout@0.2.0" as unknown as string[] },
        ["wasi:cli/stdout@0.2.0"],
      ),
    TypeError,
  );
  assert.throws(
    () =>
      instantiateComponent(
        { imports: [] },
        { imports: "wasi:cli/stdout@0.2.0" as unknown as string[] },
      ),
    TypeError,
  );
});
