export type KernelErrorCode =
  | "BUILD_ERROR"
  | "INVALID_PLACEHOLDER"
  | "MISSING_REFERENCE"
  | "CIRCULAR_REFERENCE"
  | "TRANSFORM_ERROR"
  | "INVALID_OFFSET"
  | "INVALID_RANGE"
  | "UNEDITABLE_RANGE"
  | "INVALID_STATE"
  | "REENTRANT_EDIT";

export class TemplateKernelError extends Error {
  readonly code: KernelErrorCode;
  constructor(code: KernelErrorCode, message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = "TemplateKernelError";
    this.code = code;
  }
}

export class BuildError extends TemplateKernelError {
  constructor(message: string) {
    super("BUILD_ERROR", message);
    this.name = "BuildError";
  }
}

export class InvalidPlaceholderError extends TemplateKernelError {
  constructor(message: string) {
    super("INVALID_PLACEHOLDER", message);
    this.name = "InvalidPlaceholderError";
  }
}

export class MissingReferenceError extends TemplateKernelError {
  readonly reference: string;
  constructor(reference: string, message?: string) {
    super(
      "MISSING_REFERENCE",
      message ?? `referenced placeholder "${reference}" does not exist`,
    );
    this.name = "MissingReferenceError";
    this.reference = reference;
  }
}

export class CircularReferenceError extends TemplateKernelError {
  readonly cycle: readonly string[];
  constructor(cycle: readonly string[]) {
    super(
      "CIRCULAR_REFERENCE",
      `circular dependency detected: ${cycle.join(" -> ")}`,
    );
    this.name = "CircularReferenceError";
    this.cycle = [...cycle];
  }
}

export class TransformError extends TemplateKernelError {
  readonly placeholder: string;
  readonly cause: unknown;
  constructor(placeholder: string, cause: unknown) {
    const reason = cause instanceof Error ? cause.message : String(cause);
    super(
      "TRANSFORM_ERROR",
      `pure transform of placeholder "${placeholder}" threw: ${reason}`,
      { cause },
    );
    this.name = "TransformError";
    this.cause = cause;
    this.placeholder = placeholder;
  }
}

export class InvalidOffsetError extends TemplateKernelError {
  constructor(message: string) {
    super("INVALID_OFFSET", message);
    this.name = "InvalidOffsetError";
  }
}

export class InvalidRangeError extends TemplateKernelError {
  constructor(message: string) {
    super("INVALID_RANGE", message);
    this.name = "InvalidRangeError";
  }
}

export class UneditableRangeError extends TemplateKernelError {
  constructor(message: string) {
    super("UNEDITABLE_RANGE", message);
    this.name = "UneditableRangeError";
  }
}

export class InvalidStateError extends TemplateKernelError {
  constructor(message: string) {
    super("INVALID_STATE", message);
    this.name = "InvalidStateError";
  }
}

export class ReentrantEditError extends TemplateKernelError {
  constructor(message = "re-entrant edit detected: edits are not allowed while another edit is being applied") {
    super("REENTRANT_EDIT", message);
    this.name = "ReentrantEditError";
  }
}
