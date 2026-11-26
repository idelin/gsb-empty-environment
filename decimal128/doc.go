// Package decimal128 implements a fixed-point SQL DECIMAL kernel with up to
// 38 significant digits.
//
// A Decimal128 stores a custom signed 128-bit two's-complement coefficient
// (high 64 bits signed, low 64 bits unsigned) together with SQL
// (precision, scale) metadata; the represented value is
//
//	coeff * 10^(-scale)
//
// All fixed-width arithmetic is implemented directly on 128/256-bit limbs:
// multiplication keeps the exact 256-bit product before scale reduction, and
// division uses shift-subtract long division with exact-remainder rounding.
// The math/big package is never imported by production code; it is used only
// in tests as the exact rational oracle.
//
// Rounding supports HalfAwayFromZero (commercial), HalfToEven (banker's) and
// internal Truncate. Mod follows the dividend's sign; division by zero and
// results outside [-10^38+1, 10^38-1] return explicit sentinel errors.
package decimal128
