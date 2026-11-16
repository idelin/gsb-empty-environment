#pragma once

#include <bitset>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <string>

namespace netsec {
namespace mailbox {

constexpr std::size_t kHeaderSize = 6;
constexpr uint8_t kTypeCoe = 3;
constexpr uint8_t kTypeFoe = 4;
constexpr uint8_t kFoeWriteRequest = 2;
constexpr uint8_t kCounterMin = 1;
constexpr uint8_t kCounterMax = 7;

enum class FrameDisposition {
    Accepted,
    Rejected,
    RetransmitDiscard,
};

enum class RejectReason {
    None = 0,
    TooShort,
    SmTooSmall,
    LengthExceedsSm,
    LengthExceedsReceived,
    CounterZero,
    Retransmit,
    TypeNotRegistered,
    CoeTooShort,
    CoeToggleError,
    FoeTooShort,
    FoeUnterminatedName,
    FoeEmptyName,
    FoeIllegalNameByte,
    FoePathTraversal,
    FoeEmbeddedNul,
};

struct ValidationResult {
    FrameDisposition disposition = FrameDisposition::Rejected;
    RejectReason reason = RejectReason::None;
    uint8_t mailbox_type = 0;
    uint8_t counter = 0;
    uint16_t payload_length = 0;
    const uint8_t* payload = nullptr;

    bool accepted() const {
        return disposition == FrameDisposition::Accepted;
    }
};

using LogHook = std::function<void(const std::string&)>;

// Stateful, single-mailbox-channel validator. One instance per slave mailbox
// channel; not thread-safe. Feed every received mailbox frame from the read
// callback to validate(); result payload points into the caller's buffer and
// is only valid while that buffer remains alive.
class MailboxValidator {
public:
    MailboxValidator(std::size_t syncManagerSize, LogHook logHook = nullptr)
        : sync_manager_size_(syncManagerSize), log_hook_(std::move(logHook)) {}

    void enableType(uint8_t type) {
        enabled_types_.set(type);
    }

    void disableType(uint8_t type) {
        enabled_types_.reset(type);
    }

    bool isTypeEnabled(uint8_t type) const {
        return enabled_types_.test(type);
    }

    std::size_t syncManagerSize() const {
        return sync_manager_size_;
    }

    uint8_t lastCounter() const {
        return last_counter_;
    }

    // -1 means no segment transfer is in progress.
    int expectedToggle() const {
        return expected_toggle_;
    }

    void reset() {
        last_counter_ = 0;
        expected_toggle_ = -1;
    }

    ValidationResult validate(const uint8_t* frame, std::size_t received_bytes) {
        if (frame == nullptr || received_bytes < kHeaderSize) {
            return reject(RejectReason::TooShort);
        }
        if (sync_manager_size_ < kHeaderSize) {
            return reject(RejectReason::SmTooSmall);
        }

        const uint16_t length = static_cast<uint16_t>(frame[0] |
                                                      (frame[1] << 8));
        const uint8_t type_counter_byte = frame[5];
        const uint8_t type = static_cast<uint8_t>(type_counter_byte >> 4);
        const uint8_t counter = static_cast<uint8_t>(type_counter_byte & 0x07);

        if (length > sync_manager_size_ - kHeaderSize) {
            return reject(RejectReason::LengthExceedsSm);
        }
        if (static_cast<std::size_t>(length) > received_bytes - kHeaderSize) {
            return reject(RejectReason::LengthExceedsReceived);
        }

        if (counter == 0) {
            return reject(RejectReason::CounterZero);
        }
        if (counter == last_counter_) {
            ValidationResult result;
            result.disposition = FrameDisposition::RetransmitDiscard;
            result.reason = RejectReason::Retransmit;
            result.mailbox_type = type;
            result.counter = counter;
            result.payload_length = length;
            return result;
        }
        if (last_counter_ != 0) {
            const uint8_t expected =
                (last_counter_ == kCounterMax) ? kCounterMin
                                               : static_cast<uint8_t>(last_counter_ + 1);
            if (counter != expected) {
                log("mailbox counter gap: expected " + std::to_string(expected) +
                    ", got " + std::to_string(counter));
            }
        }
        last_counter_ = counter;

        if (!isTypeEnabled(type)) {
            return reject(RejectReason::TypeNotRegistered);
        }

        ValidationResult result;
        result.disposition = FrameDisposition::Accepted;
        result.reason = RejectReason::None;
        result.mailbox_type = type;
        result.counter = counter;
        result.payload_length = length;
        result.payload = frame + kHeaderSize;

        if (type == kTypeCoe) {
            if (!validateCoe(result)) {
                result.disposition = FrameDisposition::Rejected;
                result.payload = nullptr;
            }
        } else if (type == kTypeFoe) {
            if (!validateFoe(result)) {
                result.disposition = FrameDisposition::Rejected;
                result.payload = nullptr;
            }
        }
        return result;
    }

private:
    // CoE: 2-byte CoE header (10-bit length, 2 reserved bits, 4-bit service),
    // followed by SDO data whose first byte holds the command specifier in
    // bits 7..5 and the toggle bit in bit 4.
    bool validateCoe(ValidationResult& result) {
        if (result.payload_length < 2) {
            result.reason = RejectReason::CoeTooShort;
            return false;
        }
        const uint8_t* payload = result.payload;
        if (result.payload_length < 3) {
            return true;
        }

        const uint8_t command_specifier = static_cast<uint8_t>(payload[2] >> 5);
        const uint8_t toggle = static_cast<uint8_t>((payload[2] >> 4) & 0x01);

        switch (command_specifier) {
            case 0: { // SDO segment
                const uint8_t expected =
                    (expected_toggle_ < 0) ? 0
                                           : static_cast<uint8_t>(expected_toggle_);
                if (toggle != expected) {
                    result.reason = RejectReason::CoeToggleError;
                    return false;
                }
                expected_toggle_ = toggle ^ 1;
                return true;
            }
            case 1: // SDO initiate: first following segment must toggle 0.
                expected_toggle_ = 0;
                return true;
            case 3: // SDO abort: no segment transfer in progress.
                expected_toggle_ = -1;
                return true;
            default:
                return true;
        }
    }

    // FoE: first payload byte is the opcode (3 reserved bytes follow). Only
    // write requests carry a filename at payload offset 4; the name must be
    // NUL-terminated and free of path separators, embedded NUL and ".."
    // path segments.
    bool validateFoe(ValidationResult& result) {
        if (result.payload_length < 4) {
            result.reason = RejectReason::FoeTooShort;
            return false;
        }
        const uint8_t opcode = result.payload[0];
        if (opcode != kFoeWriteRequest) {
            return true;
        }

        const uint8_t* name = result.payload + 4;
        const std::size_t capacity = result.payload_length - 4;
        if (capacity == 0 || name[capacity - 1] != 0) {
            result.reason = RejectReason::FoeUnterminatedName;
            return false;
        }
        const std::size_t name_length = capacity - 1;
        if (name_length == 0) {
            result.reason = RejectReason::FoeEmptyName;
            return false;
        }

        std::size_t segment_dots = 0;
        for (std::size_t i = 0; i < name_length; ++i) {
            const uint8_t ch = name[i];
            if (ch == 0) {
                result.reason = RejectReason::FoeEmbeddedNul;
                return false;
            }
            if (ch == '/' || ch == '\\') {
                result.reason = RejectReason::FoeIllegalNameByte;
                return false;
            }
            if (ch == '.') {
                ++segment_dots;
                if (segment_dots == 2) {
                    result.reason = RejectReason::FoePathTraversal;
                    return false;
                }
            } else {
                segment_dots = 0;
            }
        }
        return true;
    }

    ValidationResult reject(RejectReason reason) {
        ValidationResult result;
        result.disposition = FrameDisposition::Rejected;
        result.reason = reason;
        return result;
    }

    void log(const std::string& message) {
        if (log_hook_) {
            log_hook_(message);
        }
    }

    std::size_t sync_manager_size_;
    LogHook log_hook_;
    std::bitset<256> enabled_types_;
    uint8_t last_counter_ = 0;
    int expected_toggle_ = -1;
};

} // namespace mailbox
} // namespace netsec
