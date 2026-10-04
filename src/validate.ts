import type { AllocationInput } from './types.js';

export interface InputError extends Error {
  isInputError: true;
}

export function inputError(message: string): InputError {
  const err = new Error(message) as InputError;
  err.name = 'InputError';
  err.isInputError = true;
  return err;
}

export function isInputError(err: unknown): boolean {
  if (typeof err !== 'object' || err === null) return false;
  const flagged = err as { isInputError?: unknown };
  return flagged.isInputError === true;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function validateInput(input: AllocationInput): void {
  if (!isRecord(input)) throw inputError('input must be an object');
  const requests = input.requests;
  const nodes = input.nodes;
  const costs = input.costs;
  if (!Array.isArray(requests)) throw inputError('requests must be an array');
  if (!Array.isArray(nodes)) throw inputError('nodes must be an array');
  if (!isRecord(costs)) {
    throw inputError('costs must be an object of { requestRegion: { nodeRegion: cost } }');
  }

  const requestIds: Set<string> = new Set();
  for (const r of requests) {
    if (!isRecord(r)) throw inputError('each request must be an object');
    if (typeof r.id !== 'string' || r.id.length === 0) {
      throw inputError('request id must be a non-empty string');
    }
    if (requestIds.has(r.id)) throw inputError(`duplicate request id: ${r.id}`);
    requestIds.add(r.id);
    if (typeof r.region !== 'string' || r.region.length === 0) {
      throw inputError(`request ${r.id}: region must be a non-empty string`);
    }
    if (!Number.isInteger(r.demand) || r.demand < 0) {
      throw inputError(`request ${r.id}: demand must be a non-negative integer`);
    }
  }

  const nodeRegions: Set<string> = new Set();
  for (const n of nodes) {
    if (!isRecord(n)) throw inputError('each node must be an object');
    if (typeof n.id !== 'string' || n.id.length === 0) {
      throw inputError('node id must be a non-empty string');
    }
    if (nodeRegions.has(`id:${n.id}`)) throw inputError(`duplicate node id: ${n.id}`);
    nodeRegions.add(`id:${n.id}`);
    if (typeof n.region !== 'string' || n.region.length === 0) {
      throw inputError(`node ${n.id}: region must be a non-empty string`);
    }
    if (!Number.isInteger(n.capacity) || n.capacity < 0) {
      throw inputError(`node ${n.id}: capacity must be a non-negative integer`);
    }
    nodeRegions.add(`region:${n.region}`);
  }

  for (const [region, row] of Object.entries(costs)) {
    if (!isRecord(row)) throw inputError(`costs[${region}] must be an object`);
    for (const [target, value] of Object.entries(row)) {
      if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) {
        throw inputError(`costs[${region}][${target}] must be a finite number >= 0`);
      }
    }
  }

  const regions: Set<string> = new Set();
  for (const n of nodes) regions.add(n.region);
  for (const r of requests) {
    for (const nodeRegion of regions) {
      if (nodeRegion === r.region) continue;
      const row = costs[r.region];
      const value = row === undefined ? undefined : row[nodeRegion];
      if (typeof value !== 'number') {
        throw inputError(`missing cross-region cost: ${r.region} -> ${nodeRegion}`);
      }
    }
  }

  const previous = input.previous;
  if (previous !== undefined) {
    if (!isRecord(previous)) {
      throw inputError('previous must be an object mapping requestId to nodeId or null');
    }
    for (const [requestId, nodeId] of Object.entries(previous)) {
      if (!requestIds.has(requestId)) {
        throw inputError(`previous plan references unknown request id: ${requestId}`);
      }
      if (nodeId !== null && typeof nodeId !== 'string') {
        throw inputError(`previous plan for ${requestId} must be a node id string or null`);
      }
    }
  }
}
