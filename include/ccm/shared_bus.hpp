// The "hardware" boundary: a modeled external shared-memory pool plus an
// instrumented load/store path.  Reads and writes go through hooks so the
// adversarial simulator can mutate bytes precisely at load boundaries, and so
// the security monitor can observe every touch of untrusted memory.
#ifndef CCM_SHARED_BUS_HPP
#define CCM_SHARED_BUS_HPP

#include <cstring>
#include <functional>
#include <vector>

#include "ccm/status.hpp"
#include "ccm/types.hpp"

namespace ccm {

// Abstract load/store boundary.  The marshal engine and the vulnerable
// reference implementation both consume only this interface.
class IBus {
public:
    virtual ~IBus() = default;

    // Copies n external bytes at address src into dst (enclave private).
    // Returns false when the access escapes the modeled pool; such an escape
    // is also reported through the violation hook.
    virtual bool read(addr_t src, std::uint8_t* dst, len_t n) = 0;

    // Copies n private bytes into the external pool (host-visible writes).
    virtual bool write(addr_t dst, const std::uint8_t* src, len_t n) = 0;

    virtual addr_t base() const = 0;
    virtual len_t size() const = 0;
    virtual len_t read_event_count() const = 0;
};

class SharedBus : public IBus {
public:
    using AccessHook = std::function<void(addr_t address, len_t size)>;

    explicit SharedBus(addr_t base_address, len_t capacity)
        : base_(base_address), memory_(capacity, 0) {}

    addr_t base() const override { return base_; }
    len_t size() const override { return memory_.size(); }
    len_t read_event_count() const override { return read_events_; }
    len_t write_event_count() const { return write_events_; }

    // Invoked immediately before every read is serviced.  The adversary uses
    // this hook to overwrite shared bytes in the gap between two fetches.
    void set_before_read_hook(AccessHook hook) {
        before_read_ = std::move(hook);
    }
    void set_violation_hook(AccessHook hook) {
        on_violation_ = std::move(hook);
    }

    bool in_range(addr_t address, len_t n) const {
        if (address < base_) {
            return false;
        }
        addr_t offset = static_cast<addr_t>(address - base_);
        if (offset > memory_.size()) {
            return false;
        }
        return n <= (memory_.size() - static_cast<len_t>(offset));
    }

    bool read(addr_t src, std::uint8_t* dst, len_t n) override {
        ++read_events_;
        if (before_read_) {
            before_read_(src, n);
        }
        if (!in_range(src, n)) {
            if (on_violation_) {
                on_violation_(src, n);
            }
            return false;
        }
        len_t offset = static_cast<len_t>(src - base_);
        std::memcpy(dst, memory_.data() + offset, n);
        return true;
    }

    bool write(addr_t dst, const std::uint8_t* src, len_t n) override {
        ++write_events_;
        if (!in_range(dst, n)) {
            if (on_violation_) {
                on_violation_(dst, n);
            }
            return false;
        }
        len_t offset = static_cast<len_t>(dst - base_);
        std::memcpy(memory_.data() + offset, src, n);
        return true;
    }

    // Host-side helpers used while constructing attack scenarios.
    void host_poke(addr_t address, const std::uint8_t* bytes, len_t n) {
        bool ok = write(address, bytes, n);
        (void)ok;
    }
    std::uint8_t host_peek(addr_t address) const {
        return memory_[static_cast<len_t>(address - base_)];
    }
    std::uint8_t* host_raw(addr_t address) {
        return memory_.data() + static_cast<len_t>(address - base_);
    }

private:
    addr_t base_;
    std::vector<std::uint8_t> memory_;
    len_t read_events_ = 0;
    len_t write_events_ = 0;
    AccessHook before_read_;
    AccessHook on_violation_;
};

// Wraps a bus and guarantees each external byte crosses the boundary at most
// once: any fetch overlapping a previously snapshotted interval is refused.
// This turns the "single snapshot" requirement into an enforceable invariant
// instead of a coding convention.
class ReadOnceBus : public IBus {
public:
    explicit ReadOnceBus(SharedBus& bus) : bus_(bus) {}

    bool read(addr_t src, std::uint8_t* dst, len_t n) override {
        if (n == 0) {
            return bus_.read(src, dst, n);
        }
        addr_t end = static_cast<addr_t>(src + n);
        for (const Span& span : fetched_) {
            if (end > span.base && src < span.end) {
                if (on_double_read_) {
                    on_double_read_(src, n);
                }
                return false;
            }
        }
        if (!bus_.read(src, dst, n)) {
            return false;
        }
        record_span(src, end);
        return true;
    }

    bool write(addr_t dst, const std::uint8_t* src, len_t n) override {
        return bus_.write(dst, src, n);
    }

    addr_t base() const override { return bus_.base(); }
    len_t size() const override { return bus_.size(); }
    len_t read_event_count() const override {
        return bus_.read_event_count();
    }

    void set_double_read_hook(SharedBus::AccessHook hook) {
        on_double_read_ = std::move(hook);
    }

    len_t fetched_byte_count() const {
        len_t total = 0;
        for (const Span& span : fetched_) {
            total += static_cast<len_t>(span.end - span.base);
        }
        return total;
    }

private:
    struct Span {
        addr_t base;
        addr_t end;
    };

    void record_span(addr_t base, addr_t end) {
        // Keep spans sorted by base to make the intent explicit even though
        // the linear scan above already handles arbitrary ordering.
        len_t pos = fetched_.size();
        for (len_t i = 0; i < fetched_.size(); ++i) {
            if (base < fetched_[i].base) {
                pos = i;
                break;
            }
        }
        fetched_.insert(fetched_.begin() +
                            static_cast<std::vector<Span>::difference_type>(pos),
                        Span{base, end});
    }

    SharedBus& bus_;
    std::vector<Span> fetched_;
    SharedBus::AccessHook on_double_read_;
};

}  // namespace ccm

#endif  // CCM_SHARED_BUS_HPP
