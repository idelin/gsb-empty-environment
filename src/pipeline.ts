import { sanitizeDocument } from "./sanitizer.ts";
import type { SanitizeReport } from "./types.ts";

export interface PipelineDocument {
  id: string;
  html: string;
}

export interface PipelineResult {
  id: string;
  html: string;
  report: SanitizeReport;
}

// The document pipeline calls the importable sanitizer for each verdict.
export function runSanitizationPipeline(documents: PipelineDocument[]): PipelineResult[] {
  return documents.map((document) => {
    const report: SanitizeReport = { removed: [] };
    const html = sanitizeDocument(document.html, { report });
    return { id: document.id, html, report };
  });
}

export function sanitizeOne(html: string): PipelineResult {
  return runSanitizationPipeline([{ id: "single", html }])[0];
}
