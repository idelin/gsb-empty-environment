import type { TreeNodeData } from "../src/tree-store.ts";

export interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (reason?: unknown) => void;
}

export function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

export interface LoadRequest {
  parentId: string | null;
  deferred: Deferred<TreeNodeData[]>;
}

export interface ControllableLoader {
  loader: (parentId: string | null) => Promise<TreeNodeData[]>;
  requests: LoadRequest[];
  loadCount: () => number;
  requestsFor: (parentId: string | null) => LoadRequest[];
}

export function createControllableLoader(): ControllableLoader {
  const requests: LoadRequest[] = [];
  const loader = (parentId: string | null): Promise<TreeNodeData[]> => {
    const request: LoadRequest = { parentId, deferred: deferred<TreeNodeData[]>() };
    requests.push(request);
    return request.deferred.promise;
  };
  return {
    loader,
    requests,
    loadCount: () => requests.length,
    requestsFor: (parentId) => requests.filter((r) => r.parentId === parentId),
  };
}
