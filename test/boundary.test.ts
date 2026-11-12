import assert from "node:assert/strict";
import vm from "node:vm";
import { test } from "node:test";
import {
	BoundaryError,
	createBoundary,
	type BoundaryCallable,
} from "../src/boundary.ts";

const boundary = createBoundary();

interface Guest {
	context: vm.Context;
	run: (code: string) => unknown;
}

/** 构造一个与宿主隔离的 vm 领域（独立的 Object/Function/Symbol 内建对象）。 */
function createGuest(
	filename: string,
	sandbox: Record<string, unknown> = {},
): Guest {
	const context = vm.createContext(sandbox, { name: filename });
	return {
		context,
		run(code: string): unknown {
			return vm.runInContext(code, context, { filename });
		},
	};
}

/** 在 vm 领域里造一个函数并立刻通过边界包装。 */
function guestFunction(guest: Guest, body: string): BoundaryCallable {
	const raw = guest.run(`(function () { return function () { ${body} }; })()`);
	return boundary.wrapCallable(raw);
}

function assertBoundaryError(
	fn: () => unknown,
	messageMatch?: RegExp,
): BoundaryError {
	try {
		fn();
	} catch (error) {
		assert.ok(error instanceof BoundaryError, "expected a BoundaryError");
		if (messageMatch) {
			assert.match((error as Error).message, messageMatch);
		}
		assert.equal((error as BoundaryError).cause, undefined);
		return error as BoundaryError;
	}
	assert.fail("expected the call to throw a BoundaryError");
}

test("包装函数切断 constructor 链，无法取得内部 Function", () => {
	const guest = createGuest("guest-constructor.js");
	const raw = guest.run(
		`(function () {
			function internal(x) { return x + 1; }
			return internal;
		})()`,
	);

	// 先证明未防护的原始函数确实可以经 constructor 逃出 vm 领域。
	assert.notEqual((raw as Function).constructor, Function);
	const GuestFunction = (raw as Function).constructor as FunctionConstructor;
	assert.equal(new GuestFunction("return 7 + 8")(), 15);

	const wrapped = boundary.wrapCallable(raw);

	assert.equal(typeof wrapped, "function");
	assert.equal(boundary.isWrapped(wrapped), true);
	assert.equal(Object.getPrototypeOf(wrapped), null);
	assert.equal(Object.getOwnPropertyDescriptor(wrapped, "constructor"), undefined);
	assert.equal("constructor" in wrapped, false);
	assert.equal((wrapped as Record<string, unknown>).constructor, undefined);
	assert.equal((wrapped as Record<string, unknown>).__proto__, undefined);
	assert.equal(Function.prototype.isPrototypeOf(wrapped), false);
	assert.equal(Object.isFrozen(wrapped), true);

	// Function.prototype 上的方法全部不可达。
	assert.throws(() => wrapped.call(null, 1), TypeError);
	assert.throws(() => wrapped.apply(null, [1]), TypeError);
	assert.throws(() => Reflect.construct(wrapped, []), TypeError);
	assert.throws(() => {
		(wrapped as Record<string, unknown>).constructor = Function;
	}, TypeError);

	// 包装幂等，不会二次包装。
	assert.equal(boundary.wrapCallable(wrapped), wrapped);

	// 正常调用仍然可用。
	assert.equal(wrapped(41), 42);
});

test("非可调用对象在返回值、参数两个方向都被拒绝", () => {
	const guest = createGuest("guest-objects.js");

	const returnsObject = guestFunction(guest, "return { poisoned: true };");
	assertBoundaryError(() => returnsObject(), /only primitives and callables/);

	const returnsArray = guestFunction(guest, "return [1, 2, 3];");
	assertBoundaryError(() => returnsArray(), /only primitives and callables/);

	const returnsNullPrototypeObject = guestFunction(
		guest,
		"return Object.create(null);",
	);
	assertBoundaryError(
		() => returnsNullPrototypeObject(),
		/only primitives and callables/,
	);

	// 内层 guest 函数先把计数器写进领域全局，再回传参数；
	// 若参数在边界处被拦下，该计数器不会增长。
	guest.run("globalThis.__echoCalls = 0;");
	const echo = guestFunction(
		guest,
		"globalThis.__echoCalls += 1; return arguments[0];",
	);
	const countingEcho = echo;

	const beforeRejections = guest.run("globalThis.__echoCalls") as number;
	assertBoundaryError(() => countingEcho({}), /received object/);
	assertBoundaryError(() => countingEcho([1]), /received object/);
	assertBoundaryError(() => countingEcho(new Error("x")), /received object/);
	assert.equal(
		guest.run("globalThis.__echoCalls"),
		beforeRejections,
		"参数被拦在边界外，内部函数不会被调用",
	);

	// null / undefined 属于原始值，允许通过。
	assert.equal(countingEcho(null), null);
	assert.equal(countingEcho(undefined), undefined);
	assert.equal(countingEcho(42), 42);
});

test("过滤过程不读取任何属性，getter 副作用永不触发", () => {
	const guest = createGuest("getters.js");

	// 可调用值自身带恶意 getter：边界只用 typeof / WeakMap，不应触碰它们。
	const guarded = guest.run(
		`(function () {
			let reads = 0;
			function f(action) {
				if (action === "reads") return reads;
				return action;
			}
			Object.defineProperty(f, "constructor", {
				configurable: true,
				get() { reads += 1; return Function; },
			});
			Object.defineProperty(f, "payload", {
				configurable: true,
	get() { reads += 1; return { evil: true }; },
			});
			return f;
		})()`,
	);
	const wrapped = boundary.wrapCallable(guarded);
	boundary.wrap(guarded); // 命中缓存
	boundary.wrap(wrapped); // 已是硬化包装，直接返回
	assert.equal(wrapped(1), 1);
	assert.equal(wrapped("reads"), 0);
	assert.equal(wrapped(wrapped), wrapped, "硬化函数作为参数透传，同样不触发 getter");
	assert.equal(wrapped("reads"), 0);

	// callable proxy 的 get 陷阱也不会被边界探测触发。
	guest.run(
		`globalThis.__gets = 0;
		globalThis.__fn = new Proxy(
			function (action) {
				if (action === "gets") return globalThis.__gets;
				return action;
			},
			{ get(target, prop, receiver) { globalThis.__gets += 1; return Reflect.get(target, prop, receiver); } },
		);`,
	);
	const proxied = boundary.wrapCallable(guest.run("globalThis.__fn"));
	assert.equal(boundary.wrap(guest.run("globalThis.__fn")), proxied);
	assert.equal(proxied("hello"), "hello");
	assert.equal(proxied("gets"), 0);

	// 抛出的自定义对象上的 getter 同样不被读取。
	guest.run("globalThis.__sideEffects = 0;");
	const throwsTricky = guestFunction(
		guest,
		`const err = {};
		Object.defineProperty(err, "message", {
			get() { globalThis.__sideEffects += 1; throw new Error("getter escaped"); },
		});
		throw err;`,
	);
	assertBoundaryError(() => throwsTricky(), /threw object/);
	assert.equal(guest.run("globalThis.__sideEffects"), 0);
});

test("Symbol 原始值可跨界，Symbol 包装对象被拒绝", () => {
	const guest = createGuest("symbols.js");

	const describe = guestFunction(
		guest,
		"return typeof arguments[0] === 'symbol' ? arguments[0].toString() : arguments[0];",
	);
	assert.equal(describe(Symbol("host")), "Symbol(host)");

	// 跨领域共享全局 symbol 注册表，身份保持一致。
	const shared = Symbol.for("shared.key");
	const identity = guestFunction(
		guest,
		"return arguments[0] === Symbol.for('shared.key');",
	);
	assert.equal(identity(shared), true);

	// 领域内创建的 symbol 作为原始值回到宿主。
	const makeGuestSymbol = guestFunction(guest, "return Symbol('guest');");
	const guestSymbol = makeGuestSymbol();
	assert.equal(typeof guestSymbol, "symbol");
	assert.equal((guestSymbol as symbol).toString(), "Symbol(guest)");

	// 领域的 Symbol 内建对象本身是可调用值，应被包装而非原样暴露。
	const guestSymbolConstructor = guest.run("Symbol");
	assert.notEqual(guestSymbolConstructor, Symbol);
	const wrappedGuestSymbol = boundary.wrapCallable(guestSymbolConstructor);
	const made = wrappedGuestSymbol("via-wrapped-guest");
	assert.equal(typeof made, "symbol");
	assert.equal((made as symbol).toString(), "Symbol(via-wrapped-guest)");

	// new Object(symbol) 产生的是非可调用对象，拒绝跨界。
	assertBoundaryError(() => boundary.wrap(Object(Symbol("boxed"))), /received object/);
});

test("各种形状的抛出值都规范化为 BoundaryError，且不泄露原对象", () => {
	const guest = createGuest("throws.js");

	const thrower = (statement: string): BoundaryCallable =>
		guestFunction(guest, statement);

	assertBoundaryError(
		() => thrower("throw { custom: 1, nested: { a: 2 } };")(),
		/threw object/,
	);
	assertBoundaryError(() => thrower('throw new Error("boom");')(), /threw object/);
	assertBoundaryError(() => thrower("throw null;")(), /threw null/);
	assertBoundaryError(() => thrower("throw undefined;")(), /primitive undefined/);
	assertBoundaryError(() => thrower('throw "bang";')(), /primitive "bang"/);
	assertBoundaryError(() => thrower("throw 42;")(), /primitive 42/);
	assertBoundaryError(
		() => thrower("throw function () {};")(),
		/threw a callable/,
	);

	// 规范化后的错误是宿主领域的 BoundaryError，原始引用无法经 cause 泄露。
	const error = assertBoundaryError(
		() => thrower("throw { secret: 'do-not-leak' };")(),
	);
	assert.equal(error.cause, undefined);
	assert.equal(JSON.stringify(error).includes("do-not-leak"), false);
});

test("两个 vm 领域与宿主之间双向过滤，回调与身份缓存正确", () => {
	const hostEcho = boundary.wrapCallable((value: unknown) =>
		typeof value === "number" ? value * 2 : value,
	);
	const realmA = createGuest("realm-a.js", { hostEcho });
	const realmB = createGuest("realm-b.js");

	// 宿主回调注入领域 A，由领域 A 的脚本调用。
	assert.equal(realmA.run("hostEcho(21)"), 42);

	// 领域 A 的高阶函数接收宿主回调，参数经边界再次过滤。
	const higher = boundary.wrapCallable(
		realmA.run(
			`(function () { return function (fn, n) { return fn(n) + 1; }; })()`,
		),
	);
	assert.equal(higher(hostEcho, 41), 83);

	// 领域 B 的函数作为参数传给领域 A 的函数，两个 vm 领域经同一边界互通。
	const fromB = boundary.wrapCallable(
		realmB.run(
			`(function () { return function (s) { return s + '-from-b'; }; })()`,
		),
	);
	const fromA = boundary.wrapCallable(
		realmA.run(
			`(function () { return function (fn, s) { return fn(s); }; })()`,
		),
	);
	assert.equal(fromA(fromB, "hi"), "hi-from-b");

	// 同一原始函数在任意方向上包装结果一致。
	const raw = realmA.run(`(function () { return function () { return 1; }; })()`);
	assert.equal(boundary.wrap(raw), boundary.wrap(raw));

	// this 不跨界：硬化包装永远以 undefined 调用内部函数。
	const thisCheck = boundary.wrapCallable(
		realmA.run(
			`(function () { return function () { "use strict"; return this === undefined; }; })()`,
		),
	);
	assert.equal(Reflect.apply(thisCheck, { leaked: true }, []), true);

	// 各类原始值往返保持不变（含 bigint）。
	const echo = boundary.wrapCallable(
		realmB.run(`(function () { return function (v) { return v; }; })()`),
	);
	assert.equal(echo(10n), 10n);
	assert.equal(echo("text"), "text");
	assert.equal(echo(true), true);
});
