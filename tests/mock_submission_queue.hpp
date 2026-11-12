#pragma once

#include "netsec.hpp"

#include <deque>
#include <mutex>
#include <vector>

namespace netsec::testutil {

class MockSubmissionQueue : public ISubmissionQueue {
public:
    void set_reject(bool reject) { reject_ = reject; }

    bool submit(const IoDescriptor& descriptor) override {
        std::lock_guard<std::mutex> lock(mutex_);
        if (reject_) {
            ++rejected_count_;
            return false;
        }
        submitted_.push_back(descriptor);
        pending_.push_back(descriptor.lease_id);
        return true;
    }

    bool has_pending() const { return !pending_.empty(); }

    LeaseId oldest_pending() const { return pending_.front(); }

    CompletionEvent complete_oldest(std::int64_t result) {
        std::lock_guard<std::mutex> lock(mutex_);
        LeaseId id = pending_.front();
        pending_.pop_front();
        return CompletionEvent{id, result};
    }

    CompletionEvent event_for(LeaseId id, std::int64_t result) const {
        return CompletionEvent{id, result};
    }

    const std::vector<IoDescriptor>& submitted() const { return submitted_; }
    std::size_t rejected_count() const {
        std::lock_guard<std::mutex> lock(mutex_);
        return rejected_count_;
    }

private:
    mutable std::mutex mutex_;
    bool reject_ = false;
    std::size_t rejected_count_ = 0;
    std::vector<IoDescriptor> submitted_;
    std::deque<LeaseId> pending_;
};

} // namespace netsec::testutil
