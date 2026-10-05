#include "layout.hpp"

#include <utility>

namespace slayout {
namespace {

bool addChecked(uint64_t a, uint64_t b, uint64_t& out) {
    if (a > UINT64_MAX - b) return false;
    out = a + b;
    return true;
}

bool mulChecked(uint64_t a, uint64_t b, uint64_t& out) {
    if (a != 0 && b > UINT64_MAX / a) return false;
    out = a * b;
    return true;
}

bool alignUpChecked(uint64_t value, uint64_t align, uint64_t& out) {
    uint64_t rem = value % align;
    if (rem == 0) {
        out = value;
        return true;
    }
    return addChecked(value, align - rem, out);
}

bool isPowerOfTwo(uint64_t v) { return v != 0 && (v & (v - 1)) == 0; }

Layout fail(std::string msg) {
    Layout l;
    l.ok = false;
    l.error = std::move(msg);
    return l;
}

// Bytes a member occupies before the next member may start. Non-POD
// struct/union members expose their tail padding for reuse.
uint64_t footprint(const Type& t, const Layout& l) {
    return t.pod ? l.size : l.dsize;
}

std::string joinPath(const std::string& path, const std::string& name) {
    return path.empty() ? name : path + "." + name;
}

} // namespace

Layout compute(const Type& type) {
    if (!isPowerOfTwo(type.align))
        return fail("alignment of '" + type.name + "' is not a power of two");

    switch (type.kind) {
    case Kind::Scalar:
    case Kind::Padding: {
        Layout l;
        l.ok = true;
        l.size = l.dsize = type.size;
        l.align = type.kind == Kind::Padding ? 1 : type.align;
        return l;
    }
    case Kind::Array: {
        Layout elem;
        if (type.members.empty()) {
            elem.ok = true;
            elem.size = elem.dsize = type.size;
            elem.align = type.align;
        } else {
            if (type.members.size() != 1)
                return fail("array '" + type.name + "' must have exactly one element type");
            elem = compute(type.members.front());
            if (!elem.ok) return elem;
        }
        Layout l;
        l.ok = true;
        l.align = elem.align;
        if (!mulChecked(elem.size, type.count, l.size))
            return fail("integer overflow computing size of array '" + type.name + "'");
        if (type.count == 0) {
            l.dsize = 0;
        } else {
            uint64_t prefix = 0;
            if (!mulChecked(elem.size, type.count - 1, prefix) ||
                !addChecked(prefix, elem.dsize, l.dsize))
                return fail("integer overflow computing data size of array '" + type.name + "'");
        }
        l.children.push_back(std::move(elem));
        return l;
    }
    case Kind::Struct: {
        Layout l;
        l.ok = true;
        uint64_t cursor = 0;
        uint64_t maxAlign = 1;
        for (const Type& m : type.members) {
            Layout child = compute(m);
            if (!child.ok) return child;
            uint64_t offset = 0;
            if (!alignUpChecked(cursor, child.align, offset))
                return fail("integer overflow placing member '" + m.name + "'");
            FieldLayout f;
            f.name = m.name;
            f.kind = m.kind;
            f.offset = offset;
            f.size = child.size;
            f.align = child.align;
            l.fields.push_back(std::move(f));
            l.children.push_back(child);
            uint64_t next = 0;
            if (!addChecked(offset, footprint(m, child), next))
                return fail("integer overflow advancing past member '" + m.name + "'");
            cursor = next;
            if (m.kind != Kind::Padding && child.align > maxAlign)
                maxAlign = child.align;
        }
        l.dsize = cursor;
        l.align = maxAlign;
        if (!alignUpChecked(l.dsize, l.align, l.size))
            return fail("integer overflow computing size of struct '" + type.name + "'");
        return l;
    }
    case Kind::Union: {
        Layout l;
        l.ok = true;
        uint64_t maxSize = 0, maxData = 0, maxAlign = 1;
        for (const Type& m : type.members) {
            Layout child = compute(m);
            if (!child.ok) return child;
            FieldLayout f;
            f.name = m.name;
            f.kind = m.kind;
            f.offset = 0;
            f.size = child.size;
            f.align = child.align;
            l.fields.push_back(std::move(f));
            l.children.push_back(child);
            if (child.size > maxSize) maxSize = child.size;
            uint64_t fp = footprint(m, child);
            if (fp > maxData) maxData = fp;
            if (child.align > maxAlign) maxAlign = child.align;
        }
        l.align = maxAlign;
        l.dsize = maxData;
        if (!alignUpChecked(maxSize, maxAlign, l.size))
            return fail("integer overflow computing size of union '" + type.name + "'");
        return l;
    }
    }
    return fail("unknown type kind");
}

Diff firstDiff(const Layout& oldL, const Layout& newL, const std::string& path) {
    if (!oldL.ok || !newL.ok) {
        Diff d;
        d.kind = Diff::Kind::Error;
        d.path = path;
        d.detail = !oldL.ok ? "old layout: " + oldL.error : "new layout: " + newL.error;
        return d;
    }
    if (oldL.fields.size() != newL.fields.size()) {
        Diff d;
        d.kind = Diff::Kind::MemberCount;
        d.path = path;
        d.oldValue = oldL.fields.size();
        d.newValue = newL.fields.size();
        return d;
    }
    for (size_t i = 0; i < oldL.fields.size(); ++i) {
        const FieldLayout& of = oldL.fields[i];
        const FieldLayout& nf = newL.fields[i];
        std::string p = joinPath(path, nf.name);
        if (of.offset != nf.offset) {
            Diff d;
            d.kind = Diff::Kind::Offset;
            d.path = p;
            d.oldValue = of.offset;
            d.newValue = nf.offset;
            return d;
        }
        if (of.kind == Kind::Struct || of.kind == Kind::Union || of.kind == Kind::Array) {
            std::string childPath = of.kind == Kind::Array ? p + "[]" : p;
            Diff d = firstDiff(oldL.children[i], newL.children[i], childPath);
            if (d.kind != Diff::Kind::None) return d;
        }
        if (of.size != nf.size) {
            Diff d;
            d.kind = Diff::Kind::Size;
            d.path = p;
            d.oldValue = of.size;
            d.newValue = nf.size;
            return d;
        }
        if (of.align != nf.align) {
            Diff d;
            d.kind = Diff::Kind::Align;
            d.path = p;
            d.oldValue = of.align;
            d.newValue = nf.align;
            return d;
        }
    }
    if (oldL.size != newL.size) {
        Diff d;
        d.kind = Diff::Kind::Size;
        d.path = path;
        d.oldValue = oldL.size;
        d.newValue = newL.size;
        return d;
    }
    if (oldL.align != newL.align) {
        Diff d;
        d.kind = Diff::Kind::Align;
        d.path = path;
        d.oldValue = oldL.align;
        d.newValue = newL.align;
        return d;
    }
    if (oldL.dsize != newL.dsize) {
        Diff d;
        d.kind = Diff::Kind::Size;
        d.path = path;
        d.oldValue = oldL.dsize;
        d.newValue = newL.dsize;
        d.detail = "data size (tail padding excluded)";
        return d;
    }
    return Diff{};
}

std::string describe(const Diff& d) {
    std::string msg;
    switch (d.kind) {
    case Diff::Kind::None:
        return "layouts are identical";
    case Diff::Kind::Offset:
        msg = "offset changed at '" + d.path + "'";
        break;
    case Diff::Kind::Size:
        msg = "size changed at '" + d.path + "'";
        break;
    case Diff::Kind::Align:
        msg = "alignment changed at '" + d.path + "'";
        break;
    case Diff::Kind::MemberCount:
        msg = "member count changed at '" + d.path + "'";
        break;
    case Diff::Kind::Error:
        msg = "layout error at '" + d.path + "'";
        break;
    }
    if (d.kind != Diff::Kind::Error)
        msg += ": " + std::to_string(d.oldValue) + " -> " + std::to_string(d.newValue);
    if (!d.detail.empty())
        msg += " (" + d.detail + ")";
    return msg;
}

} // namespace slayout
