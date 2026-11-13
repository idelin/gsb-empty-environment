/**
 * Reporting component. It deliberately delegates all privacy checks to the
 * importable filter library so reporting and validation cannot drift apart.
 */

import {
  filterAggregateRecords,
  type AggregateFilterResult,
} from "./index.ts";

export class AggregateReporter {
  report<T = unknown>(records: Iterable<T>): AggregateFilterResult<T> {
    return filterAggregateRecords(records);
  }
}

export function reportAggregateRecords<T = unknown>(
  records: Iterable<T>,
): AggregateFilterResult<T> {
  return new AggregateReporter().report(records);
}

export const ReportingComponent = AggregateReporter;
