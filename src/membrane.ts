import { SecurityError } from './errors.ts';
import type { ExposeSchema, WrapOptions } from './types.ts';

interface BaseRecord {
  /** Original trusted target. Never leaves the membrane. */
  readonly target: object;
  /** Document token the capability was issued for. */
  readonly document: object;
  /** Revocation generation captured at issuance time. */
  readonly generation: number;
  /** Whether this branch is a restricted cross-origin membrane. */
  readonly crossOrigin: boolean;
  /** Exposure policy (only meaningful when crossOrigin is true). */
  schema: ExposeSchema;
}

interface ObjectRecord extends BaseRecord {
  readonly proxy: object;
  /**
   * Per-owner cache of member (method) proxies. Class methods live on a
   * shared prototype, so caching by target alone would bind one instance's
   * method wrapper to the wrong owner record.
   */
  readonly memberCache: WeakMap<object, object>;
}

interface FunctionRecord extends BaseRecord {
  readonly proxy: Function;
  /**
   * When the function is exposed through an object member this points at that
   * object's record, so an invoked method can validate its real receiver.
   * Undefined for root-level function capabilities.
   */
  readonly owner: ObjectRecord | undefined;
}

function isObject(value: unknown): value is object {
  return (typeof value === 'object' && value !== null) || typeof value === 'function';
}

function isFunction(value: object): value is Function {
  return typeof value === 'function';
}

/**
 * Built-in Function.prototype members must be forwarded verbatim: wrapping
 * `call`/`apply` would produce a nested proxy that no longer behaves like the
 * native call machinery (and would re-enter the apply trap).
 */
const NATIVE_FUNCTION_KEYS = new Set<string | symbol>([
  'call',
  'apply',
  'bind',
  'toString',
  Symbol.hasInstance,
]);

/**
 * Policy key describing the surface of objects a method returns. It is a
 * policy meta-key and must never be exposed as a real member name.
 */
const RETURN_KEY = '$return';

function schemaAllows(schema: ExposeSchema, key: string | symbol): boolean {
  if (schema === true) return true;
  if (schema === false) return false;
  if (key === RETURN_KEY) return false;
  return Object.prototype.hasOwnProperty.call(schema, key);
}

function childSchema(schema: ExposeSchema, key: string | symbol): ExposeSchema {
  if (schema === true) return true;
  if (schema === false) return false;
  const next = (schema as Record<string | symbol, ExposeSchema>)[key as never];
  return next ?? false;
}

function resultSchema(schema: ExposeSchema): ExposeSchema {
  if (schema === true || schema === false) return schema;
  const declared = (schema as Record<string, ExposeSchema>)[RETURN_KEY];
  return declared ?? false;
}

/**
 * A single trusted boundary. All capability identities live in WeakMaps that
 * are module-instance private: script code never receives a reference to a
 * WeakMap, a record or an original target, so no object capability can be
 * forged by copying properties or building look-alike objects.
 */
export class Membrane {
  /** Brand table: host-created proxy -> its private record. */
  readonly #brands = new WeakMap<object, ObjectRecord | FunctionRecord>();
  /**
   * Reverse table: original target -> policy key -> cached proxy.
   * The same trusted target may legitimately be handed out both as a full
   * same-origin wrapper and as a restricted cross-origin wrapper; their
   * identities and policies must never collapse into one cache entry.
   */
  readonly #targets = new WeakMap<object, Map<string, object>>();

  #generation = 0;
  #navigatedDocuments = new WeakSet<object>();

  /** Bump the revocation generation; every previously issued wrapper dies. */
  revokeAll(): void {
    this.#generation += 1;
  }

  /** Mark a document as navigated away; all wrappers bound to it die. */
  navigate(document: object): void {
    this.#navigatedDocuments.add(document);
  }

  /** Host-side recovery of the trusted target. Unavailable to scripts. */
  unwrap<T>(value: T): T {
    if (isObject(value)) {
      const record = this.#brands.get(value);
      return (record ? record.target : value) as T;
    }
    return value;
  }

  /** Brand check used by tests and host diagnostics. */
  isWrapper(value: unknown): value is object {
    return isObject(value) && this.#brands.has(value);
  }

  #cacheKey(crossOrigin: boolean, schema: ExposeSchema): string {
    return crossOrigin ? `x:${schema === true ? '*' : schema === false ? '-' : Object.keys(schema).join(',')}` : 's';
  }

  #getCached(target: object, crossOrigin: boolean, schema: ExposeSchema): object | undefined {
    return this.#targets.get(target)?.get(this.#cacheKey(crossOrigin, schema));
  }

  #putCached(target: object, crossOrigin: boolean, schema: ExposeSchema, proxy: object): void {
    let bucket = this.#targets.get(target);
    if (bucket === undefined) {
      bucket = new Map();
      this.#targets.set(target, bucket);
    }
    bucket.set(this.#cacheKey(crossOrigin, schema), proxy);
  }

  wrap<T>(target: T, document: object, options: WrapOptions = {}): T {
    if (!isObject(target)) {
      throw new TypeError('wrap() can only wrap object or function targets');
    }
    const crossOrigin = options.crossOrigin ?? false;
    const schema: ExposeSchema = crossOrigin ? (options.expose ?? false) : true;
    const existing = this.#getCached(target, crossOrigin, schema);
    if (existing !== undefined) {
      const record = this.#brands.get(existing);
      // Only reuse a live proxy for the same document; a revoked or
      // navigated-away wrapper must never be handed back as a fresh capability.
      if (
        record !== undefined &&
        record.generation === this.#generation &&
        !this.#navigatedDocuments.has(record.document) &&
        record.document === document
      ) {
        return existing as T;
      }
    }
    return this.#create(target, document, crossOrigin, schema) as T;
  }

  #create(
    target: object,
    document: object,
    crossOrigin: boolean,
    schema: ExposeSchema,
    owner: ObjectRecord | undefined = undefined,
  ): object {
    if (isFunction(target)) {
      const record: FunctionRecord = {
        target,
        document,
        generation: this.#generation,
        crossOrigin,
        schema,
        owner,
        proxy: undefined as never,
      };
      const proxy = new Proxy(target, this.#functionHandler(record));
      (record as { proxy: Function }).proxy = proxy;
      this.#brands.set(proxy, record);
      // Owner methods are cached per object record instead: the same
      // prototype function is exposed through many distinct wrappers.
      if (owner === undefined) this.#putCached(target, crossOrigin, schema, proxy);
      return proxy;
    }

    const record: ObjectRecord = {
      target,
      document,
      generation: this.#generation,
      crossOrigin,
      schema,
      proxy: undefined as never,
      memberCache: new WeakMap(),
    };
    const proxy = new Proxy(target, this.#objectHandler(record));
    (record as { proxy: object }).proxy = proxy;
    this.#brands.set(proxy, record);
    if (owner === undefined) this.#putCached(target, crossOrigin, schema, proxy);
    return proxy;
  }

  /** Liveness = current generation and a document that has not navigated. */
  #assertLive(record: BaseRecord): void {
    if (record.generation !== this.#generation) {
      throw new SecurityError('capability has been revoked');
    }
    if (this.#navigatedDocuments.has(record.document)) {
      throw new SecurityError('document has navigated; capability is no longer valid');
    }
  }

  #recordOf(receiver: object): ObjectRecord | FunctionRecord | undefined {
    return this.#brands.get(receiver);
  }

  /** Wrap (and memoize) a value reachable through a parent wrapper. */
  #wrapChild(value: unknown, parent: BaseRecord, schema: ExposeSchema = parent.schema): unknown {
    if (!isObject(value)) return value;
    const existing = this.#getCached(value, parent.crossOrigin, schema);
    if (existing !== undefined) return existing;
    return this.#create(value, parent.document, parent.crossOrigin, schema);
  }

  /** Receiver must be the exact proxy the record was issued for. */
  #isRecordProxy(receiver: object, record: ObjectRecord | FunctionRecord): boolean {
    const found = this.#brands.get(receiver);
    return found !== undefined && (found as { proxy: object }).proxy === (record as { proxy: object }).proxy;
  }

  #objectHandler(record: ObjectRecord): ProxyHandler<object> {
    const membrane = this;
    return {
      get(target, key, receiver) {
        // Defense in depth: a member read is only honored for the exact proxy
        // it was issued for. Sloppy-mode `this` merging can hand a wrapper a
        // different object (Object.prototype lookup); brand identity is what
        // matters, so compare against the record's proxy rather than `this`.
        if (!Object.is(receiver, record.proxy) && !membrane.#isRecordProxy(receiver as object, record)) {
          throw new SecurityError('illegal receiver for wrapped capability');
        }
        if (record.crossOrigin && !schemaAllows(record.schema, key)) {
          throw new SecurityError(`operation '${String(key)}' is not exposed cross-origin`);
        }
        membrane.#assertLive(record);

        const value = Reflect.get(target, key, target);
        if (!isObject(value)) return value;
        const childPolicy = childSchema(record.schema, key);

        if (isFunction(value)) {
          const cached = record.memberCache.get(value);
          if (cached !== undefined) return cached;
          const method = membrane.#create(
            value,
            record.document,
            record.crossOrigin,
            childPolicy,
            record,
          );
          record.memberCache.set(value, method);
          return method;
        }
        return membrane.#wrapChild(value, record, childPolicy);
      },

      set(target, key, value, receiver) {
        if (!Object.is(receiver, record.proxy) && !membrane.#isRecordProxy(receiver as object, record)) {
          throw new SecurityError('illegal receiver for wrapped capability');
        }
        if (record.crossOrigin) {
          throw new SecurityError('cross-origin wrappers are read-only');
        }
        membrane.#assertLive(record);
        return Reflect.set(target, key, membrane.unwrap(value), target);
      },

      has(target, key) {
        if (record.crossOrigin) return schemaAllows(record.schema, key);
        membrane.#assertLive(record);
        return Reflect.has(target, key);
      },

      deleteProperty(target, key) {
        if (record.crossOrigin) {
          throw new SecurityError('cross-origin wrappers are read-only');
        }
        membrane.#assertLive(record);
        return Reflect.deleteProperty(target, key);
      },

      defineProperty(target, key, descriptor) {
        if (record.crossOrigin) {
          throw new SecurityError('cross-origin wrappers are read-only');
        }
        membrane.#assertLive(record);
        return Reflect.defineProperty(target, key, descriptor);
      },

      ownKeys(target) {
        if (record.crossOrigin) {
          const allowed =
            record.schema === true
              ? Reflect.ownKeys(target)
              : record.schema === false
                ? []
                : Reflect.ownKeys(record.schema as object);
          return allowed.filter((key) => Reflect.has(target, key));
        }
        membrane.#assertLive(record);
        return Reflect.ownKeys(target);
      },

      getOwnPropertyDescriptor(target, key) {
        if (record.crossOrigin && !schemaAllows(record.schema, key)) return undefined;
        membrane.#assertLive(record);
        const descriptor = Reflect.getOwnPropertyDescriptor(target, key);
        if (descriptor === undefined) return undefined;
        if ('value' in descriptor && isObject(descriptor.value)) {
          return {
            ...descriptor,
            value: membrane.#wrapChild(descriptor.value, record, childSchema(record.schema, key)),
          };
        }
        // Never leak raw accessor functions: expose a data descriptor instead.
        if ('get' in descriptor) {
          return {
            enumerable: descriptor.enumerable,
            configurable: descriptor.configurable,
            writable: false,
            value: Reflect.get(target, key, target),
          };
        }
        return descriptor;
      },

      getPrototypeOf() {
        // Hide the real prototype; exposing it would hand back raw methods.
        return null;
      },

      setPrototypeOf() {
        throw new SecurityError('cannot set prototype of a wrapped capability');
      },

      preventExtensions() {
        if (record.crossOrigin) {
          throw new SecurityError('cross-origin wrappers are read-only');
        }
        membrane.#assertLive(record);
        return Reflect.preventExtensions(target);
      },

      isExtensible(target) {
        return Reflect.isExtensible(target);
      },
    };
  }

  #functionHandler(record: FunctionRecord): ProxyHandler<Function> {
    const membrane = this;
    return {
      apply(target, thisArg, argArray) {
        const receiver = thisArg ?? undefined;

        if (record.owner !== undefined) {
          // Method capability: the real receiver must be exactly the object
          // wrapper this method was issued through.
          if (
            receiver === undefined ||
            !isObject(receiver) ||
            !membrane.#isRecordProxy(receiver, record.owner)
          ) {
            throw new SecurityError('method invoked with an illegal receiver');
          }
          membrane.#assertLive(record.owner);
        } else {
          // Root function capability: unbranded or foreign-brand calls die.
          if (receiver !== undefined && membrane.#recordOf(receiver) === undefined) {
            throw new SecurityError('function invoked with an unbranded receiver');
          }
        }

        membrane.#assertLive(record);
        const args = argArray.map((arg) => membrane.unwrap(arg));
        const result = Reflect.apply(target as (...a: unknown[]) => unknown, record.owner?.target, args);
        // Objects returned by a method get the explicit `$return` policy
        // (defaulting to fully restricted when nothing is declared).
        return membrane.#wrapChild(result, record, resultSchema(record.schema));
      },

      construct(target, argArray) {
        if (record.crossOrigin) {
          throw new SecurityError('constructor is not exposed cross-origin');
        }
        membrane.#assertLive(record);
        const args = argArray.map((arg) => membrane.unwrap(arg));
        const result = Reflect.construct(target as new (...a: unknown[]) => object, args);
        return membrane.#wrapChild(result, record, resultSchema(record.schema));
      },

      get(target, key, receiver) {
        if (!Object.is(receiver, record.proxy) && !membrane.#isRecordProxy(receiver as object, record)) {
          throw new SecurityError('illegal receiver for wrapped capability');
        }
        membrane.#assertLive(record);
        // Native call machinery is forwarded verbatim; its own receiver
        // argument is validated again inside the apply trap.
        if (NATIVE_FUNCTION_KEYS.has(key)) {
          return Reflect.get(target, key, target);
        }
        if (record.crossOrigin && !schemaAllows(record.schema, key)) {
          throw new SecurityError(`operation '${String(key)}' is not exposed cross-origin`);
        }
        const value = Reflect.get(target, key, target);
        return membrane.#wrapChild(value, record, childSchema(record.schema, key));
      },

      set() {
        throw new SecurityError('cross-origin wrappers are read-only');
      },

      getPrototypeOf() {
        return null;
      },

      setPrototypeOf() {
        throw new SecurityError('cannot set prototype of a wrapped capability');
      },
    };
  }
}
