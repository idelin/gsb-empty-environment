export type TrajectoryErrorCode =
  | "INVALID_INPUT"
  | "INVALID_TRANSFORM"
  | "EMPTY_TRAJECTORY"
  | "START_OUTSIDE_REGION"
  | "END_OUTSIDE_REGION"
  | "START_INSIDE_OBSTACLE"
  | "END_INSIDE_OBSTACLE"
  | "NO_PATH"
  | "ABORTED";

export class TrajectoryRemapError extends Error {
  code: TrajectoryErrorCode;
  details?: unknown;

  constructor(code: TrajectoryErrorCode, message: string, details?: unknown) {
    super(message);
    this.name = "TrajectoryRemapError";
    this.code = code;
    this.details = details;
  }
}

export class DragCanceledError extends TrajectoryRemapError {
  constructor(message = "Drag operation was canceled") {
    super("ABORTED", message);
    this.name = "DragCanceledError";
  }
}

export function inputError(message: string, details?: unknown): TrajectoryRemapError {
  return new TrajectoryRemapError("INVALID_INPUT", message, details);
}
