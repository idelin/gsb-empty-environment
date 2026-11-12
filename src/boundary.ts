/**
 * ShadowRealm 式的跨领域（realm）调用边界。
 *
 * 边界两侧的任何对象都不能直接跨界，只有：
 *   - 原始值（string / number / bigint / boolean / symbol / undefined / null）原样通过；
 *   - 可调用值（函数，含跨 realm 的函数与 callable proxy）被包成一个无原型、
 *     无 constructor 属性的硬化函数。
 *
 * 包装函数在调用时会递归过滤参数与返回值，内部抛出的任何东西（包括自定义对象、
 * 带副作用的 getter 等）都会被规范化为一个 BoundaryError，原始错误对象
 * 永远不会越过边界。
 */

/** 跨边界调用失败时统一抛出的错误类型。 */
export class BoundaryError extends Error {
	override readonly name = "BoundaryError";

	constructor(message: string, options?: { cause?: unknown }) {
		super(message, options);
	}
}

/** 任意可调用值（包括来自其它 realm、没有共享 Function.prototype 的函数）。 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type AnyCallable = (...args: any[]) => unknown;

/** 可以跨过边界的函数：只能接收/返回原始值或其它已包装函数。 */
export type BoundaryCallable = (...args: unknown[]) => unknown;

/**
 * 一个对称的调用边界。同一个 Boundary 实例会缓存包装结果，
 * 保证同一原始函数在任意方向上都映射到同一个包装函数，且不会被二次包装。
 */
export interface Boundary {
	/**
	 * 把任意值按边界规则过滤后返回：原始值原样返回，可调用值返回硬化包装，
	 * 其它对象（数组、普通对象、Error、Date……）抛出 BoundaryError。
	 */
	wrap(value: unknown): unknown;

	/** wrap 的可调用值版本，带类型标注，调用对象时使用。 */
	wrapCallable(value: unknown): BoundaryCallable;

	/** 判断给定值是否是本边界产出的硬化包装函数。 */
	isWrapped(value: unknown): boolean;
}

/**
 * 创建一个跨领域调用边界。
 *
 * 一个实例即可双向使用：宿主把脚本领域回传的函数包起来，也可以把宿主回调
 * 包好后注入脚本领域；两个方向共用同一份过滤规则和包装缓存。
 */
export function createBoundary(): Boundary {
	// 原始函数 -> 硬化包装。WeakMap 的 key 操作不读取目标的任何属性，
	// 因此不会触发恶意 getter。
	const wrappers = new WeakMap<object, BoundaryCallable>();
	// 本边界自己产出的包装集合，用于防二次包装与 isWrapped。
	const hardened = new WeakSet<object>();

	function wrap(value: unknown): unknown {
		if (isPrimitive(value)) {
			return value;
		}

		const target = value as object;
		if (hardened.has(target)) {
			return target;
		}

		const existing = wrappers.get(target);
		if (existing !== undefined) {
			return existing;
		}

		if (typeof value !== "function") {
			throw new BoundaryError(
				`only primitives and callables may cross the boundary; received ${typeof value}`,
			);
		}

		const inner = value as AnyCallable;
		const wrapper: BoundaryCallable = (...args: unknown[]): unknown => {
			const filteredArgs = args.map(wrap);
			let result: unknown;
			try {
				// this 不跨界：永远以 undefined 调用，且参数已全部过滤。
				result = Reflect.apply(inner, undefined, filteredArgs);
			} catch (thrown) {
				throw normalizeThrown(thrown);
			}
			// 参数过滤在调用前已完成；返回值过滤本身只会抛 BoundaryError，
			// 直接透传即可，保留具体的拒绝原因。
			return wrap(result);
		};

		// 切断原型链：wrapper.constructor 与 wrapper.__proto__ 都不存在，
		// 调用方无法经 .constructor 拿到内部 Function 构造器。
		Object.setPrototypeOf(wrapper, null);
		Object.preventExtensions(wrapper);
		Object.freeze(wrapper);

		wrappers.set(target, wrapper);
		hardened.add(wrapper);
		return wrapper;
	}

	function wrapCallable(value: unknown): BoundaryCallable {
		const wrapped = wrap(value);
		if (typeof wrapped !== "function") {
			throw new BoundaryError(
				`wrapCallable expected a callable; received ${typeof value}`,
			);
		}
		return wrapped as BoundaryCallable;
	}

	function isWrapped(value: unknown): boolean {
		return typeof value === "function" && hardened.has(value as object);
	}

	return { wrap, wrapCallable, isWrapped };
}

/**
 * 把内部抛出的任意值规范化成 BoundaryError。
 *
 * 不读取 thrown 的任何属性（不访问 .message / .name，不做字符串拼接转换），
 * 避免恶意自定义对象通过 getter 制造副作用或泄露内部引用；也不挂载 cause，
 * 原始对象绝不会离开边界。
 */
function normalizeThrown(thrown: unknown): BoundaryError {
	const kind = typeof thrown;
	if (thrown === null) {
		return new BoundaryError("call across boundary failed: threw null");
	}
	if (kind === "function") {
		return new BoundaryError("call across boundary failed: threw a callable");
	}
	if (isPrimitive(thrown)) {
		return new BoundaryError(
			`call across boundary failed: threw primitive ${stringifyPrimitive(thrown)}`,
	);
	}
	return new BoundaryError(
		`call across boundary failed: threw ${kind} (cross-boundary object is not exposed)`,
	);
}

function isPrimitive(
	value: unknown,
): value is symbol | string | number | bigint | boolean | null | undefined {
	return (
		value === null ||
		(typeof value !== "object" && typeof value !== "function")
	);
}

function stringifyPrimitive(
	value: symbol | string | number | bigint | boolean | undefined,
): string {
	if (value === undefined) {
		return "undefined";
	}
	if (typeof value === "bigint") {
		return `${value.toString()}n`;
	}
	if (typeof value === "symbol") {
		return value.toString();
	}
	return JSON.stringify(value);
}
