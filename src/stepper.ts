import { Renderer } from "./renderer.ts";
import type { TreeSpec } from "./renderer.ts";

export type Action =
  | { kind: "schedule"; version: number; spec: TreeSpec }
  | { kind: "step"; count: number }
  | { kind: "drain" }
  | { kind: "unmount" };

/**
 * Explicit stepper: drives the renderer one unit at a time and can
 * replay a deterministic script of interleaved schedule/step actions.
 */
export class Stepper {
  stepsTaken = 0;
  private readonly renderer: Renderer;

  constructor(renderer: Renderer) {
    this.renderer = renderer;
  }

  /** One explicit unit of work. Returns false when the renderer is idle. */
  step(): boolean {
    const did = this.renderer.step();
    if (did) this.stepsTaken++;
    return did;
  }

  drain(): void {
    while (this.step()) {
      // keep stepping until idle
    }
  }

  replay(actions: Action[]): void {
    for (const action of actions) {
      switch (action.kind) {
        case "schedule":
          this.renderer.schedule(action.version, action.spec);
          break;
        case "step":
          for (let i = 0; i < action.count; i++) this.step();
          break;
        case "drain":
          this.drain();
          break;
        case "unmount":
          this.renderer.unmount();
          break;
      }
    }
  }
}
