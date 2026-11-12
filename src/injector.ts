import { DragCanceledError, inputError, TrajectoryRemapError } from "./errors.ts";
import {
  normalizePoint,
} from "./input.ts";
import { remapTrajectory } from "./planner.ts";
import type {
  DragEventRecord,
  ExecuteDragOptions,
  ExecuteDragResult,
  InjectorMethod,
  Point,
  PointerInjectorLike,
  RemapTrajectoryInput,
  RemapTrajectoryResult,
} from "./types.ts";

const MOVE_NAMES = [
  "moveTo",
  "move",
  "mouseMove",
  "pointerMove",
  "movePointer",
  "mouseMoveTo",
  "moveMouse",
  "moveCursor",
  "setPosition",
  "setPointerPosition",
];
const DOWN_NAMES = ["mouseDown", "down", "pointerDown", "press", "pressButton"];
const UP_NAMES = ["mouseUp", "up", "pointerUp", "release", "releaseButton", "releasePointer", "cancel", "abort"];
const SINK_NAMES = ["emit", "send", "dispatch", "sendEvent"];

type EventKind = "move" | "mousedown" | "mouseup";

interface ResolvedMethods {
  move?: { owner: object; fn: InjectorMethod };
  down?: { owner: object; fn: InjectorMethod };
  up?: { owner: object; fn: InjectorMethod };
  sink?: { owner: object; fn: InjectorMethod };
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function findMethod(owner: Record<string, unknown>, names: string[]): InjectorMethod | undefined {
  for (const name of names) {
    const candidate = owner[name];
    if (typeof candidate === "function") {
      return candidate as InjectorMethod;
    }
  }
  return undefined;
}

function resolveMethods(injector: PointerInjectorLike): ResolvedMethods {
  if (!isObject(injector)) {
    throw inputError("An injector object is required");
  }

  const roots: Record<string, unknown>[] = [injector as Record<string, unknown>];
  for (const key of ["pointer", "mouse", "input", "device", "host"]) {
    const child = (injector as Record<string, unknown>)[key];
    if (isObject(child)) {
      roots.push(child);
    }
  }

  const resolved: ResolvedMethods = {};
  for (const root of roots) {
    const move = findMethod(root, MOVE_NAMES);
    if (move && !resolved.move) {
      resolved.move = { owner: root, fn: move };
    }
    const down = findMethod(root, DOWN_NAMES);
    if (down && !resolved.down) {
      resolved.down = { owner: root, fn: down };
    }
    const up = findMethod(root, UP_NAMES);
    if (up && !resolved.up) {
      resolved.up = { owner: root, fn: up };
    }
    const sink = findMethod(root, SINK_NAMES);
    if (sink && !resolved.sink) {
      resolved.sink = { owner: root, fn: sink };
    }
  }

  if (!resolved.move && !resolved.down && !resolved.up && !resolved.sink) {
    throw inputError("Injector has no recognised move/mouse-down/mouse-up methods");
  }
  return resolved;
}

function invokePointMethod(
  method: { owner: object; fn: InjectorMethod },
  point: Point,
  index: number,
): unknown {
  const arity = method.fn.length;
  if (arity >= 2) {
    return method.fn.call(method.owner, point.x, point.y, point, index);
  }
  if (arity === 1) {
    return method.fn.call(method.owner, point);
  }
  return method.fn.call(method.owner);
}

function invokeButtonMethod(
  method: { owner: object; fn: InjectorMethod },
  point: Point,
  index: number,
): unknown {
  const arity = method.fn.length;
  if (arity >= 2) {
    return method.fn.call(method.owner, point.x, point.y, point, index);
  }
  if (arity === 1) {
    return method.fn.call(method.owner, point);
  }
  return method.fn.call(method.owner);
}

function invokeSink(
  method: { owner: object; fn: InjectorMethod },
  kind: EventKind,
  point: Point,
  index: number,
): unknown {
  const event = {
    type: kind,
    x: point.x,
    y: point.y,
    point: { x: point.x, y: point.y },
    index,
  };
  return method.fn.call(method.owner, event);
}

function moveOperation(
  methods: ResolvedMethods,
  point: Point,
  index: number,
): unknown {
  if (methods.move) {
    return invokePointMethod(methods.move, point, index);
  }
  if (methods.sink) {
    return invokeSink(methods.sink, "move", point, index);
  }
  return undefined;
}

function downOperation(
  methods: ResolvedMethods,
  point: Point,
  index: number,
): unknown {
  if (methods.down) {
    return invokeButtonMethod(methods.down, point, index);
  }
  if (methods.sink) {
    return invokeSink(methods.sink, "mousedown", point, index);
  }
  throw new TrajectoryRemapError("INVALID_INPUT", "Injector has no mouse-down operation");
}

function upOperation(
  methods: ResolvedMethods,
  point: Point,
  index: number,
): unknown {
  if (methods.up) {
    return invokeButtonMethod(methods.up, point, index);
  }
  if (methods.sink) {
    return invokeSink(methods.sink, "mouseup", point, index);
  }
  throw new TrajectoryRemapError("INVALID_INPUT", "Injector has no mouse-up/release operation");
}

function isPromiseLike(value: unknown): value is PromiseLike<unknown> {
  return isObject(value) && typeof (value as { then?: unknown }).then === "function";
}

function extractPath(value: unknown): Point[] {
  const plainPoints = (points: unknown[], label: string): Point[] =>
    points.map((point, index) => {
      const normalized = normalizePoint(point, `${label}[${index}]`);
      return { x: normalized.x, y: normalized.y };
    });

  if (Array.isArray(value)) {
    return plainPoints(value, "path");
  }

  if (isObject(value)) {
    const objectValue = value as Record<string, unknown>;
    for (const key of ["path", "plannedPath", "points", "trajectory"]) {
      if (Array.isArray(objectValue[key])) {
        return plainPoints(objectValue[key] as unknown[], "path");
      }
    }
  }

  throw inputError("executeDrag expects a path array or a plan/remap result");
}

function samePoint(a: Point, b: Point): boolean {
  return a.x === b.x && a.y === b.y;
}

function abortError(): DragCanceledError {
  return new DragCanceledError();
}

export async function executeDrag(
  pathOrPlan: unknown,
  injector: PointerInjectorLike,
  options: ExecuteDragOptions = {},
): Promise<ExecuteDragResult> {
  const path = extractPath(pathOrPlan);
  if (path.length === 0) {
    throw inputError("Drag path is empty");
  }

  const methods = resolveMethods(injector);
  const events: DragEventRecord[] = [];
  let moved = 0;
  let pressed = false;
  let released = false;
  let mousedownCount = 0;
  let mouseupCount = 0;
  let canceled = false;
  let lastPoint: Point = path[0];

  // Let a just-created drag session observe an immediate cancel before any
  // pointer event is injected.
  await Promise.resolve();

  const checkAbort = (): void => {
    if (options.signal?.aborted) {
      canceled = true;
      throw abortError();
    }
  };

  const release = async (): Promise<void> => {
    if (!pressed || released) {
      return;
    }
    released = true;
    const result = upOperation(methods, lastPoint, Math.max(0, path.length - 1));
    if (isPromiseLike(result)) {
      await result;
    }
    mouseupCount += 1;
    events.push({
      type: "mouseup",
      x: lastPoint.x,
      y: lastPoint.y,
      point: { x: lastPoint.x, y: lastPoint.y },
      index: Math.max(0, path.length - 1),
    });
  };

  try {
    checkAbort();
    if (options.moveToStart !== false) {
      lastPoint = path[0];
      const result = moveOperation(methods, lastPoint, 0);
      if (isPromiseLike(result)) {
        await result;
      }
      moved += 1;
      events.push({ type: "move", x: lastPoint.x, y: lastPoint.y, point: { ...lastPoint }, index: 0 });
    }

    checkAbort();
    pressed = true;
    const downResult = downOperation(methods, lastPoint, 0);
    if (isPromiseLike(downResult)) {
      await downResult;
    }
    mousedownCount += 1;
    events.push({ type: "mousedown", x: lastPoint.x, y: lastPoint.y, point: { ...lastPoint }, index: 0 });

    for (let index = 1; index < path.length; index += 1) {
      checkAbort();
      const point = path[index];
      if (samePoint(point, lastPoint)) {
        continue;
      }
      lastPoint = point;
      const result = moveOperation(methods, point, index);
      if (isPromiseLike(result)) {
        await result;
      }
      moved += 1;
      events.push({ type: "move", x: point.x, y: point.y, point: { ...point }, index });
    }

    checkAbort();
    lastPoint = path[path.length - 1];
    await release();
    return {
      ok: true,
      path,
      moved,
      mousedownCount,
      mouseupCount,
      canceled,
      events,
    };
  } catch (error) {
    if (pressed && !released) {
      try {
        await release();
      } catch {
        // Preserve the original failure; the release attempt still happened.
      }
    }
    if (error instanceof DragCanceledError && options.throwOnCancel === false) {
      return {
        ok: true,
        path,
        moved,
        mousedownCount,
        mouseupCount,
        canceled: true,
        events,
      };
    }
    if (options.signal?.aborted && !(error instanceof DragCanceledError)) {
      throw abortError();
    }
    throw error;
  }
}

export function executeDragSync(
  pathOrPlan: unknown,
  injector: PointerInjectorLike,
  options: ExecuteDragOptions = {},
): ExecuteDragResult {
  const path = extractPath(pathOrPlan);
  if (path.length === 0) {
    throw inputError("Drag path is empty");
  }

  const methods = resolveMethods(injector);
  const events: DragEventRecord[] = [];
  let moved = 0;
  let pressed = false;
  let released = false;
  let mousedownCount = 0;
  let mouseupCount = 0;
  let canceled = false;
  let lastPoint: Point = path[0];

  const ensureSync = (value: unknown): void => {
    if (isPromiseLike(value)) {
      throw inputError("Injector returned a Promise during executeDragSync; use executeDrag instead");
    }
  };
  const checkAbort = (): void => {
    if (options.signal?.aborted) {
      canceled = true;
      throw abortError();
    }
  };
  const release = (): void => {
    if (!pressed || released) {
      return;
    }
    released = true;
    ensureSync(upOperation(methods, lastPoint, Math.max(0, path.length - 1)));
    mouseupCount += 1;
    events.push({
      type: "mouseup",
      x: lastPoint.x,
      y: lastPoint.y,
      point: { x: lastPoint.x, y: lastPoint.y },
      index: Math.max(0, path.length - 1),
    });
  };

  try {
    checkAbort();
    if (options.moveToStart !== false) {
      lastPoint = path[0];
      ensureSync(moveOperation(methods, lastPoint, 0));
      moved += 1;
      events.push({ type: "move", x: lastPoint.x, y: lastPoint.y, point: { ...lastPoint }, index: 0 });
    }

    checkAbort();
    pressed = true;
    ensureSync(downOperation(methods, lastPoint, 0));
    mousedownCount += 1;
    events.push({ type: "mousedown", x: lastPoint.x, y: lastPoint.y, point: { ...lastPoint }, index: 0 });

    for (let index = 1; index < path.length; index += 1) {
      checkAbort();
      const point = path[index];
      if (samePoint(point, lastPoint)) {
        continue;
      }
      lastPoint = point;
      ensureSync(moveOperation(methods, point, index));
      moved += 1;
      events.push({ type: "move", x: point.x, y: point.y, point: { ...point }, index });
    }

    checkAbort();
    lastPoint = path[path.length - 1];
    release();
    return {
      ok: true,
      path,
      moved,
      mousedownCount,
      mouseupCount,
      canceled,
      events,
    };
  } catch (error) {
    if (pressed && !released) {
      try {
        release();
      } catch {
        // Preserve the original failure.
      }
    }
    if (error instanceof DragCanceledError && options.throwOnCancel === false) {
      return {
        ok: true,
        path,
        moved,
        mousedownCount,
        mouseupCount,
        canceled: true,
        events,
      };
    }
    if (options.signal?.aborted && !(error instanceof DragCanceledError)) {
      throw abortError();
    }
    throw error;
  }
}

export class RecordingInjector implements PointerInjectorLike {
  events: DragEventRecord[];

  constructor() {
    this.events = [];
  }

  moveTo(point: Point): void {
    this.events.push({ type: "move", x: point.x, y: point.y, point: { x: point.x, y: point.y } });
  }

  mouseDown(point: Point): void {
    this.events.push({ type: "mousedown", x: point.x, y: point.y, point: { x: point.x, y: point.y } });
  }

  mouseUp(point: Point): void {
    this.events.push({ type: "mouseup", x: point.x, y: point.y, point: { x: point.x, y: point.y } });
  }

  cancel(point?: Point): void {
    this.events.push({
      type: "cancel",
      x: point?.x,
      y: point?.y,
      point: point ? { x: point.x, y: point.y } : undefined,
    });
  }

  get recordedEvents(): DragEventRecord[] {
    return this.events;
  }
}

export function createRecordingInjector(): RecordingInjector {
  return new RecordingInjector();
}

export function executeDragPath(
  pathOrPlan: unknown,
  injector: PointerInjectorLike,
  options?: ExecuteDragOptions,
): Promise<ExecuteDragResult> {
  return executeDrag(pathOrPlan, injector, options);
}

export function runDrag(
  pathOrPlan: unknown,
  injector: PointerInjectorLike,
  options?: ExecuteDragOptions,
): Promise<ExecuteDragResult> {
  return executeDrag(pathOrPlan, injector, options);
}

export function performDrag(
  pathOrPlan: unknown,
  injector: PointerInjectorLike,
  options?: ExecuteDragOptions,
): Promise<ExecuteDragResult> {
  return executeDrag(pathOrPlan, injector, options);
}

export const executeDragOperation = executeDrag;
export const executeDragTrajectory = executeDrag;

export interface DragSession {
  promise: Promise<ExecuteDragResult>;
  done: Promise<ExecuteDragResult>;
  signal: AbortSignal;
  cancel: () => void;
}

export function createDragSession(
  pathOrPlan: unknown,
  injector: PointerInjectorLike,
  options: ExecuteDragOptions = {},
): DragSession {
  const controller = new AbortController();
  if (options.signal) {
    if (options.signal.aborted) {
      controller.abort();
    } else {
      options.signal.addEventListener("abort", () => controller.abort(), { once: true });
    }
  }

  const promise = executeDrag(pathOrPlan, injector, { ...options, signal: controller.signal });
  return {
    promise,
    done: promise,
    signal: controller.signal,
    cancel: () => controller.abort(),
  };
}

export function startDrag(
  pathOrPlan: unknown,
  injector: PointerInjectorLike,
  options?: ExecuteDragOptions,
): DragSession {
  return createDragSession(pathOrPlan, injector, options);
}

export async function remapAndExecute(
  input: RemapTrajectoryInput,
  injector: PointerInjectorLike,
  options?: ExecuteDragOptions,
): Promise<{ remap: RemapTrajectoryResult; execution: ExecuteDragResult }> {
  const remap = remapTrajectory(input);
  const execution = await executeDrag(remap.path, injector, options);
  return { remap, execution };
}
