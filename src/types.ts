/**
 * Affinity of an anchor to the text around it.
 *
 * Anchors live on document boundaries (the gaps between characters).
 * When text is inserted exactly at an anchor's boundary, the affinity
 * decides on which side of the inserted text the anchor ends up:
 *  - 'left':  the anchor stays before the inserted text.
 *  - 'right': the anchor moves after the inserted text.
 */
export type Affinity = 'left' | 'right';

/**
 * A single edit expressed in ORIGINAL document coordinates.
 * Replaces the range [start, end) with `text`.
 *  - start === end:  pure insertion of `text` at that boundary.
 *  - text === '':    pure deletion of [start, end).
 */
export interface Edit {
  start: number;
  end: number;
  text: string;
}

/** An anchor attached to a document boundary. */
export interface Anchor {
  id: string;
  pos: number;
  affinity: Affinity;
}

/** A selection with both ends expressed as document boundaries. */
export interface Selection {
  start: number;
  end: number;
}

/** Optional per-end affinities used when transforming a selection. */
export interface SelectionAffinities {
  start?: Affinity;
  end?: Affinity;
}
