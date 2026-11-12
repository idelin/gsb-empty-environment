// Package obsmerge merges partial page-scraping observations into per-business-key
// records while keeping every field's provenance.
//
// A scraping page usually exposes only a subset of the fields of an entity.
// A field missing from an observation means "not observed by this page", not
// "the value was deleted". Only an explicitly covered field that arrives as the
// zero value (Nullable.Null) deletes a previously observed value.
//
// Each [Observation] carries:
//   - PageType: the kind of page that was scraped;
//   - CoveredFields: the field set that page actually provides;
//   - BusinessKey: the entity the observation belongs to;
//   - SourceID/SourceVersion: where the observation came from;
//   - Seq: ingestion order, used only to supersede earlier observations from the
//     same source and to make replay deterministic.
//
// Merge rules:
//   - Fields outside CoveredFields are never touched and stay [StatusUnknown]
//     until some observation covers them.
//   - A covered field contributes evidence; a Null covered field contributes
//     explicit-null evidence that can delete earlier values.
//   - Sources are merged per field by trust priority (higher priority wins).
//   - On equal priority, evidence never overwrites earlier evidence based on
//     arrival order: the field becomes [StatusConflict] and every tied piece of
//     evidence is retained.
//   - A newer observation from the same source supersedes that source's earlier
//     evidence for the same key/field.
//
// An incorrect observation can be retracted with [Engine.Retract], which
// recomputes the affected business key from the remaining observations.
// [Engine.ReplayAndVerify] folds every retained observation from scratch and
// checks that the incremental result matches the pure replay, reporting any
// difference together with the source of every output field.
package obsmerge
