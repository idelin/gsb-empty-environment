#ifndef BORROW_BYTE_INTERPRETER_HPP
#define BORROW_BYTE_INTERPRETER_HPP

/*
 * byte_interpreter.hpp
 *
 * A deliberately small object model used to exercise the authority.  Each
 * logical access is checked byte by byte against the authority before the
 * byte is touched.  This is a verification tool, not part of the embeddable
 * core; the core itself never opens a pointer.
 */

#include "borrow_authority.hpp"

#include <cstdint>
#include <map>
#include <optional>
#include <string>
#include <vector>

namespace borrow {

struct ByteReadResult {
    Status status = Status::Ok;
    std::uint8_t value = 0;
    Offset failed_at = 0;
    std::string detail;
    explicit operator bool() const { return status == Status::Ok; }
};

struct ByteWriteResult {
    Status status = Status::Ok;
    Offset failed_at = 0;
    std::string detail;
    explicit operator bool() const { return status == Status::Ok; }
};

struct ByteSpanResult {
    Status status = Status::Ok;
    std::vector<std::uint8_t> bytes;
    Offset failed_at = 0;
    std::string detail;
    explicit operator bool() const { return status == Status::Ok; }
};

struct ByteMoveResult {
    Status status = Status::Ok;
    ObjectRef ref;
    std::string detail;
    explicit operator bool() const { return status == Status::Ok; }
};

class ByteMemory {
public:
    explicit ByteMemory(Authority& authority) : authority_(authority) {}

    ObjectRef create(ObjectId id, Offset size, Generation generation = 0) {
        ObjectRef ref = authority_.define_object(id, size, generation);
        bytes_[id].assign(static_cast<std::size_t>(size), 0);
        if (id >= next_id_) next_id_ = id + 1;
        return ref;
    }

    ObjectRef create_auto(Offset size) { return create(next_id_, size); }

    bool has(ObjectId id) const { return bytes_.count(id) != 0; }

    const std::vector<std::uint8_t>* bytes(ObjectId id) const {
        auto it = bytes_.find(id);
        return it == bytes_.end() ? nullptr : &it->second;
    }

    void fill(ObjectRef ref, std::uint8_t value) {
        auto* data = mutable_bytes(ref);
        if (data == nullptr) return;
        for (std::uint8_t& byte : *data) byte = value;
    }

    ByteReadResult read_byte(FrameId frame, ObjectRef ref, Offset offset) const {
        ByteReadResult out;
        auto it = bytes_.find(ref.id);
        if (it == bytes_.end()) {
            out.status = Status::NotFound;
            out.detail = "small object is not registered";
            return out;
        }
        if (!authority_.is_current(ref)) {
            out.status = Status::StaleGeneration;
            out.detail = "old generation after move";
            return out;
        }
        if (offset >= it->second.size()) {
            out.status = Status::OutOfBounds;
            out.failed_at = offset;
            out.detail = "byte index exceeds the small object";
            return out;
        }
        AccessCheck check = authority_.can_access(
            frame, ref, Range(offset, offset + 1), AccessKind::Read);
        if (!check.allowed) {
            out.status = check.status;
            out.failed_at = offset;
            out.detail = check.detail;
            return out;
        }
        out.value = it->second[static_cast<std::size_t>(offset)];
        return out;
    }

    ByteWriteResult write_byte(FrameId frame, ObjectRef ref, Offset offset,
                               std::uint8_t value) {
        ByteWriteResult out;
        auto it = bytes_.find(ref.id);
        if (it == bytes_.end()) {
            out.status = Status::NotFound;
            out.detail = "small object is not registered";
            return out;
        }
        if (!authority_.is_current(ref)) {
            out.status = Status::StaleGeneration;
            out.detail = "old generation after move";
            return out;
        }
        if (offset >= it->second.size()) {
            out.status = Status::OutOfBounds;
            out.failed_at = offset;
            out.detail = "byte index exceeds the small object";
            return out;
        }
        AccessCheck check = authority_.can_access(
            frame, ref, Range(offset, offset + 1), AccessKind::Write);
        if (!check.allowed) {
            out.status = check.status;
            out.failed_at = offset;
            out.detail = check.detail;
            return out;
        }
        it->second[static_cast<std::size_t>(offset)] = value;
        return out;
    }

    ByteSpanResult read(FrameId frame, ObjectRef ref, Range range) const {
        ByteSpanResult out;
        auto it = bytes_.find(ref.id);
        if (it == bytes_.end()) {
            out.status = Status::NotFound;
            out.detail = "small object is not registered";
            return out;
        }
        if (!authority_.is_current(ref)) {
            out.status = Status::StaleGeneration;
            out.detail = "old generation after move";
            return out;
        }
        if (range.begin > range.end || range.end > it->second.size()) {
            out.status = Status::OutOfBounds;
            out.failed_at = range.begin;
            out.detail = "range exceeds the small object";
            return out;
        }
        out.bytes.reserve(static_cast<std::size_t>(range.size()));
        for (Offset offset = range.begin; offset < range.end; ++offset) {
            ByteReadResult one = read_byte(frame, ref, offset);
            if (!one) {
                out.status = one.status;
                out.failed_at = offset;
                out.detail = one.detail;
                return out;
            }
            out.bytes.push_back(one.value);
        }
        return out;
    }

    ByteWriteResult write(FrameId frame, ObjectRef ref, Range range,
                          const std::vector<std::uint8_t>& values) {
        ByteWriteResult out;
        auto it = bytes_.find(ref.id);
        if (it == bytes_.end()) {
            out.status = Status::NotFound;
            out.detail = "small object is not registered";
            return out;
        }
        if (!authority_.is_current(ref)) {
            out.status = Status::StaleGeneration;
            out.detail = "old generation after move";
            return out;
        }
        if (range.size() != values.size() || range.end > it->second.size()) {
            out.status = Status::OutOfBounds;
            out.failed_at = range.begin;
            out.detail = "range and value lengths disagree";
            return out;
        }
        for (Offset offset = range.begin; offset < range.end; ++offset) {
            const std::size_t index = static_cast<std::size_t>(offset - range.begin);
            ByteWriteResult one =
                write_byte(frame, ref, offset, values[index]);
            if (!one) {
                out.status = one.status;
                out.failed_at = offset;
                out.detail = one.detail;
                return out;
            }
        }
        return out;
    }

    ByteMoveResult move(FrameId frame, ObjectRef ref) {
        ByteMoveResult out;
        auto it = bytes_.find(ref.id);
        if (it == bytes_.end()) {
            out.status = Status::NotFound;
            out.detail = "small object is not registered";
            return out;
        }
        const ObjectInfo* info = authority_.object(ref.id);
        if (info == nullptr || info->generation != ref.generation) {
            out.status = Status::StaleGeneration;
            out.detail = "old generation after move";
            return out;
        }
        AccessCheck check =
            authority_.can_move(frame, ref, Range(0, info->size));
        if (!check.allowed) {
            out.status = check.status;
            out.detail = check.detail;
            return out;
        }
        auto fresh = authority_.move_object(ref.id);
        if (!fresh.has_value()) {
            out.status = Status::NotFound;
            out.detail = "move failed";
            return out;
        }
        out.ref = *fresh;
        return out;
    }

    // Checks every byte independently; the returned set contains exactly the
    // byte-sized intervals that the authority allows.
    IntervalSet accessible_bytes(FrameId frame, ObjectRef ref, AccessKind kind,
                                 Range within) const {
        IntervalSet out;
        auto it = bytes_.find(ref.id);
        if (it == bytes_.end()) return out;
        const Offset limit = std::min<Offset>(within.end, it->second.size());
        for (Offset offset = within.begin; offset < limit; ++offset) {
            if (authority_
                    .can_access(frame, ref, Range(offset, offset + 1), kind)
                    .allowed) {
                out.add(Range(offset, offset + 1));
            }
        }
        return out;
    }

    std::optional<Offset> first_forbidden_byte(FrameId frame, ObjectRef ref,
                                               AccessKind kind,
                                               Range within) const {
        auto it = bytes_.find(ref.id);
        if (it == bytes_.end()) return within.begin;
        const Offset limit = std::min<Offset>(within.end, it->second.size());
        for (Offset offset = within.begin; offset < limit; ++offset) {
            if (!authority_
                     .can_access(frame, ref, Range(offset, offset + 1), kind)
                     .allowed) {
                return offset;
            }
        }
        return std::nullopt;
    }

    std::string hex(ObjectId id) const {
        static const char* digits = "0123456789abcdef";
        auto it = bytes_.find(id);
        if (it == bytes_.end()) return {};
        std::string out;
        out.reserve(it->second.size() * 2);
        for (std::uint8_t byte : it->second) {
            out.push_back(digits[byte >> 4]);
            out.push_back(digits[byte & 0x0f]);
        }
        return out;
    }

private:
    std::vector<std::uint8_t>* mutable_bytes(ObjectRef ref) {
        if (!authority_.is_current(ref)) return nullptr;
        auto it = bytes_.find(ref.id);
        return it == bytes_.end() ? nullptr : &it->second;
    }

    Authority& authority_;
    std::map<ObjectId, std::vector<std::uint8_t>> bytes_;
    ObjectId next_id_ = 1;
};

}  // namespace borrow

#endif  // BORROW_BYTE_INTERPRETER_HPP
