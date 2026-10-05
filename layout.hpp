#pragma once

#include <cstdint>
#include <string>
#include <vector>

// Compiler-independent binary layout engine.
//
// A Type tree describes a struct/union purely by field sizes, alignments,
// array lengths and explicit padding. compute() derives offsets, size,
// alignment and data size (size without tail padding) algorithmically,
// never consulting the host compiler's layout. firstDiff() compares two
// computed layouts and reports the first offset, size or alignment change
// in declaration order.

namespace slayout {

enum class Kind { Scalar, Array, Struct, Union, Padding };

struct Type {
    Kind kind = Kind::Scalar;
    std::string name;
    uint64_t size = 0;    // Scalar/Padding: byte size. Array without element child: element size.
    uint64_t align = 1;   // byte alignment, must be a power of two
    uint64_t count = 1;   // Array: element count
    bool pod = true;      // Struct/Union: POD types never let a following member reuse tail padding
    std::vector<Type> members;  // Struct/Union members; Array: optional single element type
};

struct FieldLayout {
    std::string name;
    Kind kind = Kind::Scalar;
    uint64_t offset = 0;
    uint64_t size = 0;    // sizeof the member as placed (arrays: total)
    uint64_t align = 1;
};

struct Layout {
    bool ok = false;
    std::string error;    // set when ok == false (e.g. integer overflow)
    uint64_t size = 0;    // sizeof: includes tail padding
    uint64_t dsize = 0;   // data size: excludes tail padding (reusable when embedded non-POD)
    uint64_t align = 1;
    std::vector<FieldLayout> fields;
    std::vector<Layout> children;  // parallel to fields; Struct/Union/Array members have child entries
};

Layout compute(const Type& type);

struct Diff {
    enum class Kind { None, Offset, Size, Align, MemberCount, Error } kind = Kind::None;
    std::string path;     // dotted path, e.g. "Packet.inner.field"
    uint64_t oldValue = 0;
    uint64_t newValue = 0;
    std::string detail;
};

Diff firstDiff(const Layout& oldLayout, const Layout& newLayout, const std::string& path);
std::string describe(const Diff& diff);

} // namespace slayout
