// layout_checker.hpp
//
// 结构布局检查器：根据字段大小、对齐、数组长度与显式填充描述，
// 独立计算二进制布局（不依赖当前编译器），并比较新旧两版布局，
// 报告首个偏移、大小或对齐变化。支持嵌套结构与联合体，
// 所有算术均做 64 位整数溢出检查。
//
// C++17，头文件库，无外部依赖。

#pragma once

#include <cstdint>
#include <limits>
#include <memory>
#include <optional>
#include <string>
#include <utility>
#include <vector>

namespace layoutcheck {

struct Type;
using TypePtr = std::shared_ptr<const Type>;

// 结构/联合体的一个成员。explicitPadding 标记显式填充字节。
struct Member {
    std::string name;
    TypePtr type;
    bool explicitPadding = false;
};

struct Type {
    enum class Kind { Primitive, Array, Struct, Union };

    Kind kind = Kind::Primitive;
    // Primitive：大小与对齐（对齐必须为 2 的幂）。
    uint64_t size = 0;
    uint64_t align = 0;
    // Array：元素类型与长度。
    TypePtr element;
    uint64_t count = 0;
    // Struct / Union：按声明顺序排列的成员。
    std::vector<Member> members;
};

// ---------------------------------------------------------------------------
// 类型构造辅助
// ---------------------------------------------------------------------------

inline TypePtr primitive(uint64_t size, uint64_t align) {
    auto t = std::make_shared<Type>();
    t->kind = Type::Kind::Primitive;
    t->size = size;
    t->align = align;
    return t;
}

inline TypePtr arrayOf(TypePtr element, uint64_t count) {
    auto t = std::make_shared<Type>();
    t->kind = Type::Kind::Array;
    t->element = std::move(element);
    t->count = count;
    return t;
}

inline TypePtr structOf(std::vector<Member> members) {
    auto t = std::make_shared<Type>();
    t->kind = Type::Kind::Struct;
    t->members = std::move(members);
    return t;
}

inline TypePtr unionOf(std::vector<Member> members) {
    auto t = std::make_shared<Type>();
    t->kind = Type::Kind::Union;
    t->members = std::move(members);
    return t;
}

inline Member field(std::string name, TypePtr type) {
    return Member{std::move(name), std::move(type), false};
}

// 显式填充：占据 bytes 字节、对齐为 1 的填充成员。
inline Member padField(std::string name, uint64_t bytes) {
    return Member{std::move(name), primitive(bytes, 1), true};
}

// ---------------------------------------------------------------------------
// 布局计算（全部溢出安全）
// ---------------------------------------------------------------------------

struct Layout {
    uint64_t size = 0;
    uint64_t align = 1;
    std::vector<uint64_t> memberOffsets; // 与 members 一一对应；联合体成员全为 0
};

namespace detail {

inline bool isPowerOfTwo(uint64_t v) { return v != 0 && (v & (v - 1)) == 0; }

inline std::optional<uint64_t> alignUp(uint64_t value, uint64_t align) {
    const uint64_t max = std::numeric_limits<uint64_t>::max();
    const uint64_t mask = align - 1;
    if (value > max - mask) return std::nullopt;
    return (value + mask) & ~mask;
}

} // namespace detail

// 计算布局；任何一步发生 64 位溢出或描述非法时返回 std::nullopt。
inline std::optional<Layout> computeLayout(const Type& t) {
    const uint64_t max = std::numeric_limits<uint64_t>::max();
    switch (t.kind) {
    case Type::Kind::Primitive: {
        if (t.size == 0 || !detail::isPowerOfTwo(t.align)) return std::nullopt;
        Layout l;
        l.size = t.size;
        l.align = t.align;
        return l;
    }
    case Type::Kind::Array: {
        if (!t.element) return std::nullopt;
        auto elem = computeLayout(*t.element);
        if (!elem) return std::nullopt;
        // 元素大小已含其尾部填充，直接相乘，并检查乘法溢出。
        if (t.count != 0 && elem->size > max / t.count) return std::nullopt;
        Layout l;
        l.size = elem->size * t.count;
        l.align = elem->align;
        return l;
    }
    case Type::Kind::Struct: {
        Layout l;
        l.align = 1;
        uint64_t offset = 0;
        for (const Member& m : t.members) {
            if (!m.type) return std::nullopt;
            auto ml = computeLayout(*m.type);
            if (!ml) return std::nullopt;
            auto aligned = detail::alignUp(offset, ml->align);
            if (!aligned) return std::nullopt;
            offset = *aligned;
            l.memberOffsets.push_back(offset);
            if (ml->size > max - offset) return std::nullopt;
            offset += ml->size;
            if (ml->align > l.align) l.align = ml->align;
        }
        // 尾部填充：总大小向上取整到最大对齐。
        auto size = detail::alignUp(offset, l.align);
        if (!size) return std::nullopt;
        l.size = *size;
        return l;
    }
    case Type::Kind::Union: {
        Layout l;
        l.align = 1;
        uint64_t size = 0;
        for (const Member& m : t.members) {
            if (!m.type) return std::nullopt;
            auto ml = computeLayout(*m.type);
            if (!ml) return std::nullopt;
            l.memberOffsets.push_back(0);
            if (ml->size > size) size = ml->size;
            if (ml->align > l.align) l.align = ml->align;
        }
        auto alignedSize = detail::alignUp(size, l.align);
        if (!alignedSize) return std::nullopt;
        l.size = *alignedSize;
        return l;
    }
    }
    return std::nullopt;
}

// ---------------------------------------------------------------------------
// 布局比较
// ---------------------------------------------------------------------------

struct Diff {
    enum class Kind {
        Offset,       // 成员偏移变化
        Size,         // 类型大小变化
        Align,        // 对齐要求变化
        Count,        // 数组长度变化
        KindMismatch, // 类型类别变化（如 Primitive -> Struct）
        FieldRemoved, // 新版本中字段被移除
        FieldAdded,   // 新版本中字段被新增
        Overflow      // 布局计算发生 64 位溢出
    };

    Kind kind;
    std::string path;      // 形如 "outer.inner.field" / "arr[]"
    uint64_t oldValue = 0; // Offset/Size/Align/Count 的旧值
    uint64_t newValue = 0; // 对应的新值
    std::string message;
};

inline const char* toString(Diff::Kind k) {
    switch (k) {
    case Diff::Kind::Offset: return "offset";
    case Diff::Kind::Size: return "size";
    case Diff::Kind::Align: return "align";
    case Diff::Kind::Count: return "count";
    case Diff::Kind::KindMismatch: return "kind-mismatch";
    case Diff::Kind::FieldRemoved: return "field-removed";
    case Diff::Kind::FieldAdded: return "field-added";
    case Diff::Kind::Overflow: return "overflow";
    }
    return "unknown";
}

namespace detail {

inline std::string joinPath(const std::string& base, const std::string& name) {
    return base.empty() ? name : base + "." + name;
}

inline Diff makeDiff(Diff::Kind kind, std::string path, uint64_t oldV, uint64_t newV) {
    Diff d;
    d.kind = kind;
    d.path = std::move(path);
    d.oldValue = oldV;
    d.newValue = newV;
    switch (kind) {
    case Diff::Kind::Offset:
        d.message = "offset of '" + d.path + "' changed: " +
                    std::to_string(oldV) + " -> " + std::to_string(newV);
        break;
    case Diff::Kind::Size:
        d.message = "size of '" + d.path + "' changed: " +
                    std::to_string(oldV) + " -> " + std::to_string(newV);
        break;
    case Diff::Kind::Align:
        d.message = "alignment of '" + d.path + "' changed: " +
                    std::to_string(oldV) + " -> " + std::to_string(newV);
        break;
    case Diff::Kind::Count:
        d.message = "array length of '" + d.path + "' changed: " +
                    std::to_string(oldV) + " -> " + std::to_string(newV);
        break;
    case Diff::Kind::KindMismatch:
        d.message = "type kind of '" + d.path + "' changed";
        break;
    case Diff::Kind::FieldRemoved:
        d.message = "field '" + d.path + "' was removed";
        break;
    case Diff::Kind::FieldAdded:
        d.message = "field '" + d.path + "' was added";
        break;
    case Diff::Kind::Overflow:
        d.message = "layout of '" + d.path + "' overflows 64-bit arithmetic";
        break;
    }
    return d;
}

// 递归比较。path 为当前类型在顶层结构中的路径。
inline std::optional<Diff> compareRec(const Type& oldT, const Type& newT,
                                      const std::string& path) {
    if (oldT.kind != newT.kind)
        return makeDiff(Diff::Kind::KindMismatch, path,
                        static_cast<uint64_t>(oldT.kind),
                        static_cast<uint64_t>(newT.kind));

    switch (oldT.kind) {
    case Type::Kind::Primitive:
        if (oldT.size != newT.size)
            return makeDiff(Diff::Kind::Size, path, oldT.size, newT.size);
        if (oldT.align != newT.align)
            return makeDiff(Diff::Kind::Align, path, oldT.align, newT.align);
        return std::nullopt;

    case Type::Kind::Array: {
        if (oldT.count != newT.count)
            return makeDiff(Diff::Kind::Count, path, oldT.count, newT.count);
        if (!oldT.element || !newT.element)
            return makeDiff(Diff::Kind::KindMismatch, path, 0, 0);
        return compareRec(*oldT.element, *newT.element, path + "[]");
    }

    case Type::Kind::Struct:
    case Type::Kind::Union: {
        auto oldLayout = computeLayout(oldT);
        auto newLayout = computeLayout(newT);
        if (!oldLayout || !newLayout)
            return makeDiff(Diff::Kind::Overflow, path, 0, 0);

        // 成员按名字匹配（字段可能在新版本中重排、增删）。
        std::vector<bool> matched(newT.members.size(), false);
        for (size_t i = 0; i < oldT.members.size(); ++i) {
            const Member& om = oldT.members[i];
            size_t j = newT.members.size();
            for (size_t k = 0; k < newT.members.size(); ++k) {
                if (!matched[k] && newT.members[k].name == om.name) {
                    j = k;
                    break;
                }
            }
            const std::string memberPath = joinPath(path, om.name);
            if (j == newT.members.size())
                return makeDiff(Diff::Kind::FieldRemoved, memberPath, 0, 0);
            matched[j] = true;
            if (oldT.kind == Type::Kind::Struct &&
                oldLayout->memberOffsets[i] != newLayout->memberOffsets[j]) {
                return makeDiff(Diff::Kind::Offset, memberPath,
                                oldLayout->memberOffsets[i],
                                newLayout->memberOffsets[j]);
            }
            if (!om.type || !newT.members[j].type)
                return makeDiff(Diff::Kind::KindMismatch, memberPath, 0, 0);
            auto d = compareRec(*om.type, *newT.members[j].type, memberPath);
            if (d) return d;
        }
        for (size_t j = 0; j < newT.members.size(); ++j) {
            if (!matched[j]) {
                return makeDiff(Diff::Kind::FieldAdded,
                                joinPath(path, newT.members[j].name), 0, 0);
            }
        }
        if (oldLayout->size != newLayout->size)
            return makeDiff(Diff::Kind::Size, path, oldLayout->size,
                            newLayout->size);
        if (oldLayout->align != newLayout->align)
            return makeDiff(Diff::Kind::Align, path, oldLayout->align,
                            newLayout->align);
        return std::nullopt;
    }
    }
    return std::nullopt;
}

} // namespace detail

// 比较新旧两版类型布局，返回首个差异；完全一致时返回 std::nullopt。
inline std::optional<Diff> compareLayouts(const Type& oldT, const Type& newT,
                                          std::string rootPath = "<root>") {
    return detail::compareRec(oldT, newT, std::move(rootPath));
}

} // namespace layoutcheck
