export const MAX_LINKS = 25;

export type ObservationStatus = "ok" | "fail";

export interface Observation {
  id: string;
  path: string[];
  status: ObservationStatus;
}

export interface Problem {
  links: string[];
  observations: Observation[];
}

export class InputError extends Error {}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseLinks(raw: unknown): string[] {
  if (!isRecord(raw) || !Array.isArray(raw.links)) {
    throw new InputError('topology must be an object with a "links" array');
  }
  const links: string[] = [];
  for (const entry of raw.links) {
    if (typeof entry === "string") {
      links.push(entry);
    } else if (isRecord(entry) && typeof entry.id === "string") {
      links.push(entry.id);
    } else {
      throw new InputError(
        'each link must be a string id or an object with a string "id"',
      );
    }
  }
  const seen = new Set<string>();
  for (const id of links) {
    if (seen.has(id)) {
      throw new InputError(`duplicate link id: ${id}`);
    }
    seen.add(id);
  }
  if (links.length > MAX_LINKS) {
    throw new InputError(
      `too many links: ${links.length} (maximum supported is ${MAX_LINKS})`,
    );
  }
  return links;
}

function parseObservations(raw: unknown, knownLinks: Set<string>): Observation[] {
  if (!Array.isArray(raw)) {
    throw new InputError('"observations" must be an array');
  }
  const observations: Observation[] = [];
  const seenIds = new Set<string>();
  for (const entry of raw) {
    if (!isRecord(entry)) {
      throw new InputError("each observation must be an object");
    }
    if (typeof entry.id !== "string" || entry.id.length === 0) {
      throw new InputError('each observation needs a non-empty string "id"');
    }
    if (seenIds.has(entry.id)) {
      throw new InputError(`duplicate observation id: ${entry.id}`);
    }
    seenIds.add(entry.id);
    if (entry.status !== "ok" && entry.status !== "fail") {
      throw new InputError(
        `observation ${entry.id}: status must be "ok" or "fail"`,
      );
    }
    if (!Array.isArray(entry.path)) {
      throw new InputError(`observation ${entry.id}: "path" must be an array`);
    }
    const path: string[] = [];
    for (const hop of entry.path) {
      if (typeof hop !== "string" || !knownLinks.has(hop)) {
        throw new InputError(
          `observation ${entry.id}: unknown link in path: ${String(hop)}`,
        );
      }
      if (!path.includes(hop)) {
        path.push(hop);
      }
    }
    observations.push({ id: entry.id, path, status: entry.status });
  }
  return observations;
}

export function parseProblem(raw: unknown): Problem {
  if (!isRecord(raw)) {
    throw new InputError("input must be a JSON object");
  }
  const links = parseLinks(raw.topology);
  const observations = parseObservations(raw.observations, new Set(links));
  return { links, observations };
}
