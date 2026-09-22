// Package bitlayout compiles JSON record-layout descriptions into reusable
// encode and incremental-decode plans.
//
// The supported subset is deliberately small: fixed-width integers, arrays
// whose length is given by a previous field, unions selected by a previous
// tag, alignment padding, and checksum fields covering a specified encoded
// range. Bit order and byte order are independent schema properties.
//
// Compile rejects forward length references, dependency cycles, duplicate
// field names, undefined union tags and unbounded allocations. Encode
// validates the whole object and the destination capacity before writing a
// single bit. Decode sessions may stop at any bit, distinguish missing
// input, illegal reserved bits and illegal values, report the schema path
// and input bit offset of every failure, and never deliver half a record.
//
// A compiled Plan is immutable and safe for concurrent use; each Session
// keeps its own cursor and quota accounting.
package bitlayout
