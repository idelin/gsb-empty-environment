import { copyRect, toTSV } from "./clipboard.ts";
import type { GridNode } from "./fixtures/nodeTree.ts";
import { computeLayout, hitTest, type GridGeometry } from "./layout.ts";
import { MergeMap } from "./merge.ts";
import { Navigator, type Direction } from "./navigation.ts";
import { effectiveSelection, type Selection } from "./selection.ts";
import type { Pos, Rect } from "./types.ts";

/**
 * Facade over layout, navigation, selection and clipboard for one
 * grid document (node-tree fixture + glyph fixture).
 */
export class Grid {
  readonly merges: MergeMap;
  readonly geometry: GridGeometry;
  readonly doc: GridNode;
  private readonly values: string[][];
  private readonly navigator: Navigator;

  constructor(doc: GridNode) {
    this.doc = doc;
    this.merges = new MergeMap(doc.merges);
    this.geometry = computeLayout(doc);
    this.values = doc.children.map((rowNode) =>
      rowNode.children.map((cellNode) => cellNode.text),
    );
    this.navigator = new Navigator(this.merges, this.geometry.rows, this.geometry.cols);
  }

  get rows(): number {
    return this.geometry.rows;
  }

  get cols(): number {
    return this.geometry.cols;
  }

  valueAt(pos: Pos): string {
    return this.values[pos.row]?.[pos.col] ?? "";
  }

  /** Arrow-key move; the result is never a covered cell. */
  move(from: Pos, direction: Direction): Pos {
    return this.navigator.move(from, direction);
  }

  /** Pixel hit-test against the fixture-computed geometry. */
  cellAtPoint(x: number, y: number): Pos | null {
    return hitTest(this.geometry, x, y);
  }

  /** Selection rect expanded to fully cover every merge it touches. */
  select(selection: Selection): Rect {
    return effectiveSelection(selection, this.merges);
  }

  /** Copy the expanded selection as a value matrix. */
  copyMatrix(selection: Selection): string[][] {
    return copyRect(this.select(selection), this.merges, (pos) => this.valueAt(pos));
  }

  /** Copy the expanded selection as TSV for the clipboard. */
  copyTSV(selection: Selection): string {
    return toTSV(this.copyMatrix(selection));
  }
}
