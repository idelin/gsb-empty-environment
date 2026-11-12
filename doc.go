// Package taskpkg reconstructs text lines from quadrilateral OCR word boxes.
//
// The package is deliberately independent of any OCR engine. A host supplies
// the page rotation, word quads, text and confidence; BuildLines normalizes the
// geometry, removes duplicate recognitions, validates possible neighbours and
// chooses the minimum-cost ordered line partition.
package taskpkg
