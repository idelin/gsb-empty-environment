/**
 * Rich-text attribute intervals over a plain string.
 *
 * - All public offsets are UTF-16 code-unit offsets (JS string indices).
 * - Edits may never split a surrogate pair; attempting to do so throws.
 * - Three attribute families with distinct conflict rules:
 *     bold     — boolean, freely overlapping; adjacent equal ranges merge.
 *     link     — non-overlapping; a new link covers its whole range and the
 *                old link keeps its non-overlapping residual segments.
 *     entity   — non-overlapping and indivisible; editing inside an entity
 *                invalidates the whole entity and reports a reason.
 * - Serialization is canonical: sorted intervals, merged bold, fixed key
 *   order — identical logical states serialize identically regardless of
 *   the order operations were applied in.
 */

export interface Range {
  start: number;
  end: number;
}

export interface LinkInterval extends Range {
  href: string;
}

export interface EntityInterval extends Range {
  kind: string;
  id?: string;
}

export type EntityInvalidationReason =
  | "insert-inside-entity"
  | "delete-intersects-entity"
  | "replaced-by-new-entity";

export interface EntityInvalidatedEvent {
  type: "entity-invalidated";
  entity: EntityInterval;
  reason: EntityInvalidationReason;
}

export type EditEvent = EntityInvalidatedEvent;

export interface SerializedDocument {
  text: string;
  bold: Range[];
  links: LinkInterval[];
  entities: EntityInterval[];
}

function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/** Throw if `offset` falls between the two halves of a surrogate pair. */
function assertPairBoundary(text: string, offset: number, what: string): void {
  if (offset <= 0 || offset >= text.length) return;
  const before = text.charCodeAt(offset - 1);
  const after = text.charCodeAt(offset);
  if (isHighSurrogate(before) && isLowSurrogate(after)) {
    throw new RangeError(
      `${what} splits a surrogate pair at UTF-16 offset ${offset}`,
    );
  }
}

function compareRanges(a: Range, b: Range): number {
  return a.start - b.start || a.end - b.end;
}

/** Merge overlapping or adjacent ranges. Input may be unsorted. */
function mergeRanges(ranges: Range[]): Range[] {
  const sorted = ranges.slice().sort(compareRanges);
  const out: Range[] = [];
  for (const r of sorted) {
    const last = out[out.length - 1];
    if (last && r.start <= last.end) {
      last.end = Math.max(last.end, r.end);
    } else {
      out.push({ start: r.start, end: r.end });
    }
  }
  return out;
}

/** Subtract [start, end) from every range; drop empties. */
function subtractRanges(ranges: Range[], start: number, end: number): Range[] {
  const out: Range[] = [];
  for (const r of ranges) {
    if (r.end <= start || r.start >= end) {
      out.push({ ...r });
      continue;
    }
    if (r.start < start) out.push({ start: r.start, end: start });
    if (r.end > end) out.push({ start: end, end: r.end });
  }
  return out;
}

/**
 * Canonicalize links: sort and merge adjacent ranges carrying the same
 * href, so equal logical states always serialize identically.
 */
function normalizeLinks(links: LinkInterval[]): LinkInterval[] {
  const sorted = links.slice().sort(compareRanges);
  const out: LinkInterval[] = [];
  for (const link of sorted) {
    const last = out[out.length - 1];
    if (last && last.href === link.href && link.start <= last.end) {
      last.end = Math.max(last.end, link.end);
    } else {
      out.push({ ...link });
    }
  }
  return out;
}

export class RichTextDocument {
  #text: string;
  #bold: Range[] = [];
  #links: LinkInterval[] = [];
  #entities: EntityInterval[] = [];

  constructor(text = "") {
    this.#text = text;
  }

  get length(): number {
    return this.#text.length;
  }

  getText(): string {
    return this.#text;
  }

  getBold(): Range[] {
    return this.#bold.map((r) => ({ ...r }));
  }

  getLinks(): LinkInterval[] {
    return this.#links.map((r) => ({ ...r }));
  }

  getEntities(): EntityInterval[] {
    return this.#entities.map((r) => ({ ...r }));
  }

  #assertRange(start: number, end: number, what: string): void {
    if (
      !Number.isInteger(start) ||
      !Number.isInteger(end) ||
      start < 0 ||
      end > this.#text.length ||
      start >= end
    ) {
      throw new RangeError(
        `${what}: invalid range [${start}, ${end}) for length ${this.#text.length}`,
      );
    }
    assertPairBoundary(this.#text, start, what);
    assertPairBoundary(this.#text, end, what);
  }

  // ---------------------------------------------------------------- edits

  insert(offset: number, inserted: string): EditEvent[] {
    if (!Number.isInteger(offset) || offset < 0 || offset > this.#text.length) {
      throw new RangeError(`insert: invalid offset ${offset}`);
    }
    assertPairBoundary(this.#text, offset, "insert");
    if (inserted.length === 0) return [];

    const events: EditEvent[] = [];
    const len = inserted.length;

    this.#entities = this.#entities.filter((entity) => {
      if (entity.start < offset && offset < entity.end) {
        events.push({
          type: "entity-invalidated",
          entity: { ...entity },
          reason: "insert-inside-entity",
        });
        return false;
      }
      if (offset <= entity.start) {
        entity.start += len;
        entity.end += len;
      }
      return true;
    });

    const shift = <T extends Range>(r: T): T => {
      if (r.end <= offset) return r;
      if (r.start >= offset) {
        r.start += len;
        r.end += len;
      } else {
        r.end += len; // insertion inside the range expands it
      }
      return r;
    };
    this.#bold = mergeRanges(this.#bold.map(shift));
    this.#links = normalizeLinks(this.#links.map(shift));

    this.#text =
      this.#text.slice(0, offset) + inserted + this.#text.slice(offset);
    return events;
  }

  delete(start: number, end: number): EditEvent[] {
    this.#assertRange(start, end, "delete");
    const events: EditEvent[] = [];
    const len = end - start;

    this.#entities = this.#entities.filter((entity) => {
      const intersects = entity.start < end && entity.end > start;
      if (!intersects) {
        if (entity.start >= end) {
          entity.start -= len;
          entity.end -= len;
        }
        return true;
      }
      const fullyCovered = start <= entity.start && entity.end <= end;
      if (!fullyCovered) {
        events.push({
          type: "entity-invalidated",
          entity: { ...entity },
          reason: "delete-intersects-entity",
        });
      }
      return false; // covered entities vanish with their text
    });

    const clip = <T extends Range>(ranges: T[]): T[] => {
      const out: T[] = [];
      for (const r of ranges) {
        if (r.end <= start) {
          out.push(r);
        } else if (r.start >= end) {
          r.start -= len;
          r.end -= len;
          out.push(r);
        } else {
          if (r.start < start) out.push({ ...r, end: start });
          if (r.end > end) out.push({ ...r, start, end: r.end - len });
        }
      }
      return out.filter((r) => r.start < r.end);
    };
    this.#bold = mergeRanges(clip(this.#bold));
    this.#links = normalizeLinks(clip(this.#links));

    this.#text = this.#text.slice(0, start) + this.#text.slice(end);
    return events;
  }

  // ----------------------------------------------------------- attributes

  setBold(start: number, end: number, on = true): void {
    this.#assertRange(start, end, "setBold");
    if (on) {
      this.#bold = mergeRanges([...this.#bold, { start, end }]);
    } else {
      this.#bold = mergeRanges(subtractRanges(this.#bold, start, end));
    }
  }

  /**
   * Apply a link. Links never overlap: the new link always covers its full
   * range; existing links keep only their non-overlapping residual segments.
   */
  setLink(start: number, end: number, href: string): void {
    this.#assertRange(start, end, "setLink");
    const kept: LinkInterval[] = [];
    for (const link of this.#links) {
      for (const piece of subtractRanges([link], start, end)) {
        kept.push({ ...piece, href: link.href });
      }
    }
    kept.push({ start, end, href });
    this.#links = normalizeLinks(kept);
  }

  removeLink(start: number, end: number): void {
    this.#assertRange(start, end, "removeLink");
    const kept: LinkInterval[] = [];
    for (const link of this.#links) {
      for (const piece of subtractRanges([link], start, end)) {
        kept.push({ ...piece, href: link.href });
      }
    }
    this.#links = normalizeLinks(kept);
  }

  /**
   * Apply an indivisible entity. Entities never overlap: any existing entity
   * intersecting the range is replaced as a whole (never partially).
   */
  setEntity(
    start: number,
    end: number,
    kind: string,
    id?: string,
  ): EditEvent[] {
    this.#assertRange(start, end, "setEntity");
    const events: EditEvent[] = [];
    this.#entities = this.#entities.filter((entity) => {
      if (entity.start < end && entity.end > start) {
        events.push({
          type: "entity-invalidated",
          entity: { ...entity },
          reason: "replaced-by-new-entity",
        });
        return false;
      }
      return true;
    });
    const entity: EntityInterval = { start, end, kind };
    if (id !== undefined) entity.id = id;
    this.#entities.push(entity);
    this.#entities.sort(compareRanges);
    return events;
  }

  // --------------------------------------------------------- serialization

  /** Canonical snapshot: sorted intervals, merged bold, fixed key order. */
  toJSON(): SerializedDocument {
    return {
      text: this.#text,
      bold: this.getBold(),
      links: this.getLinks(),
      entities: this.getEntities(),
    };
  }

  /**
   * Canonical serialization. Identical logical states produce identical
   * strings no matter in which order edits and attributes were applied.
   */
  serialize(): string {
    return JSON.stringify(this.toJSON());
  }
}
