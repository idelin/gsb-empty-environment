#ifndef PACBTI_LABEL_HPP
#define PACBTI_LABEL_HPP

#include <array>
#include <cstdint>
#include <map>
#include <vector>

namespace pacbti {

constexpr std::uint8_t kRegCount = 32;
constexpr std::uint16_t kInvalidType = 0xFFFFu;
constexpr std::uint16_t kSpReg = 31u;
constexpr std::uint32_t kInvalidSite = 0xFFFFFFFFu;

enum class KeyDomain : std::uint8_t {
    None = 0,
    IA,
    IB,
    DA,
    DB
};

enum class LabelKind : std::uint8_t {
    Bottom = 0,
    Unknown,
    Raw,
    Signed,
    Poisoned
};

using OriginList = std::vector<std::uint32_t>;

struct Label {
    LabelKind kind = LabelKind::Unknown;
    KeyDomain key = KeyDomain::None;
    std::uint16_t typeId = kInvalidType;
    bool attackerTainted = false;
    OriginList defOrigins;
    OriginList modifierOrigins;
    OriginList signOrigins;
    OriginList poisonOrigins;

    static Label bottom() {
        Label label;
        label.kind = LabelKind::Bottom;
        return label;
    }

    static Label unknown() {
        Label label;
        label.kind = LabelKind::Unknown;
        return label;
    }

    static Label raw(std::uint16_t ptrType, bool tainted) {
        Label label;
        label.kind = LabelKind::Raw;
        label.key = KeyDomain::None;
        label.typeId = ptrType;
        label.attackerTainted = tainted;
        return label;
    }

    static Label signedPtr(KeyDomain domain,
                           std::uint16_t ptrType,
                           bool tainted,
                           OriginList modifiers,
                           OriginList signs) {
        Label label;
        label.kind = LabelKind::Signed;
        label.key = domain;
        label.typeId = ptrType;
        label.attackerTainted = tainted;
        label.modifierOrigins = std::move(modifiers);
        label.signOrigins = std::move(signs);
        return label;
    }

    static Label poisoned(OriginList origins) {
        Label label;
        label.kind = LabelKind::Poisoned;
        label.key = KeyDomain::None;
        label.typeId = kInvalidType;
        label.attackerTainted = true;
        label.poisonOrigins = std::move(origins);
        return label;
    }

    bool isBottom() const { return kind == LabelKind::Bottom; }
    bool isUnknown() const { return kind == LabelKind::Unknown; }
    bool isRaw() const { return kind == LabelKind::Raw; }
    bool isSigned() const { return kind == LabelKind::Signed; }
    bool isPoisoned() const { return kind == LabelKind::Poisoned; }
    bool hasSignOrigin() const { return !signOrigins.empty(); }
};

inline bool sameOrigins(const OriginList& lhs, const OriginList& rhs) {
    return lhs == rhs;
}

inline OriginList mergeOrigins(OriginList lhs, const OriginList& rhs) {
    for (std::uint32_t site : rhs) {
        bool present = false;
        for (std::uint32_t existing : lhs) {
            if (existing == site) {
                present = true;
                break;
            }
        }
        if (!present) {
            lhs.push_back(site);
        }
    }
    return lhs;
}

inline bool labelsEqual(const Label& lhs, const Label& rhs) {
    return lhs.kind == rhs.kind &&
           lhs.key == rhs.key &&
           lhs.typeId == rhs.typeId &&
           lhs.attackerTainted == rhs.attackerTainted &&
           sameOrigins(lhs.defOrigins, rhs.defOrigins) &&
           sameOrigins(lhs.modifierOrigins, rhs.modifierOrigins) &&
           sameOrigins(lhs.signOrigins, rhs.signOrigins) &&
           sameOrigins(lhs.poisonOrigins, rhs.poisonOrigins);
}

inline Label mergeLabels(const Label& lhs, const Label& rhs) {
    if (lhs.isBottom()) {
        return rhs;
    }
    if (rhs.isBottom()) {
        return lhs;
    }
    if (labelsEqual(lhs, rhs)) {
        return lhs;
    }
    if (lhs.isUnknown() || rhs.isUnknown()) {
        return Label::unknown();
    }
    if (lhs.kind != rhs.kind) {
        return Label::unknown();
    }

    Label result;
    result.kind = lhs.kind;
    result.key = lhs.key == rhs.key ? lhs.key : KeyDomain::None;
    result.typeId = lhs.typeId == rhs.typeId ? lhs.typeId : kInvalidType;
    result.attackerTainted = lhs.attackerTainted || rhs.attackerTainted;
    result.defOrigins = mergeOrigins(lhs.defOrigins, rhs.defOrigins);
    result.modifierOrigins = mergeOrigins(lhs.modifierOrigins, rhs.modifierOrigins);
    result.signOrigins = mergeOrigins(lhs.signOrigins, rhs.signOrigins);
    result.poisonOrigins = mergeOrigins(lhs.poisonOrigins, rhs.poisonOrigins);
    return result;
}

struct AbstractState {
    std::array<Label, kRegCount> regs{};
    std::map<std::uint16_t, Label> stack;

    static AbstractState entry() {
        AbstractState state;
        for (Label& label : state.regs) {
            label = Label::unknown();
        }
        return state;
    }

    Label getReg(std::uint8_t reg) const {
        return regs.at(static_cast<std::size_t>(reg));
    }

    void setReg(std::uint8_t reg, Label label) {
        regs.at(static_cast<std::size_t>(reg)) = std::move(label);
    }

    Label getStack(std::uint16_t slot) const {
        auto it = stack.find(slot);
        if (it == stack.end()) {
            return Label::unknown();
        }
        return it->second;
    }

    void setStack(std::uint16_t slot, Label label) {
        stack[slot] = std::move(label);
    }
};

inline bool statesEqual(const AbstractState& lhs, const AbstractState& rhs) {
    for (std::uint8_t reg = 0; reg < kRegCount; ++reg) {
        if (!labelsEqual(lhs.getReg(reg), rhs.getReg(reg))) {
            return false;
        }
    }
    if (lhs.stack.size() != rhs.stack.size()) {
        return false;
    }
    for (const auto& entry : lhs.stack) {
        auto it = rhs.stack.find(entry.first);
        if (it == rhs.stack.end() || !labelsEqual(entry.second, it->second)) {
            return false;
        }
    }
    return true;
}

inline AbstractState joinStates(const AbstractState& lhs, const AbstractState& rhs) {
    AbstractState result;
    for (std::uint8_t reg = 0; reg < kRegCount; ++reg) {
        result.setReg(reg, mergeLabels(lhs.getReg(reg), rhs.getReg(reg)));
    }

    std::map<std::uint16_t, Label> merged = lhs.stack;
    for (const auto& entry : rhs.stack) {
        auto it = merged.find(entry.first);
        if (it == merged.end()) {
            merged[entry.first] = entry.second;
        } else {
            it->second = mergeLabels(it->second, entry.second);
        }
    }
    result.stack = std::move(merged);
    return result;
}

}  // namespace pacbti

#endif
