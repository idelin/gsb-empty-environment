#include "netsec.hpp"

#include <random>
#include <stdexcept>
#include <utility>

namespace netsec {
namespace {

SecurityAccessStateMachine::SeedSource makeDefaultSeedSource() {
    return [] {
        std::random_device random;
        Seed seed(4);
        for (auto& byte : seed) {
            byte = static_cast<std::uint8_t>(random());
        }
        return seed;
    };
}

}

SecurityAccessStateMachine::SecurityAccessStateMachine()
    : SecurityAccessStateMachine(makeDefaultSeedSource()) {}

SecurityAccessStateMachine::SecurityAccessStateMachine(
    SeedSource seedSource, Clock clock, std::uint8_t initialSession)
    : defaultSeedSource_(std::move(seedSource)),
      clock_(std::move(clock)),
      currentSession_(initialSession) {
    if (!defaultSeedSource_) {
        throw std::invalid_argument("a seed source is required");
    }
}

void SecurityAccessStateMachine::registerLevel(
    std::uint8_t requestSeedSubfunction, KeyVerifier verifier,
    SeedSource seedSource) {
    if (!isSeedSubfunction(requestSeedSubfunction)) {
        throw std::invalid_argument(
            "a security level must be registered by its odd seed subfunction");
    }
    if (!verifier) {
        throw std::invalid_argument("a key verifier is required");
    }

    LevelState level;
    level.verifier = std::move(verifier);
    level.seedSource = std::move(seedSource);
    levels_[requestSeedSubfunction] = std::move(level);
}

void SecurityAccessStateMachine::registerLevel(
    std::uint8_t requestSeedSubfunction, Key expectedKey,
    SeedSource seedSource) {
    registerLevel(
        requestSeedSubfunction,
        [expectedKey = std::move(expectedKey)](const Seed&, const Key& key) {
            return key == expectedKey;
        },
        std::move(seedSource));
}

SecurityAccessResult SecurityAccessStateMachine::handle(
    std::uint8_t subfunction, const Key& key) {
    if (isSeedSubfunction(subfunction)) {
        return requestSeed(subfunction);
    }
    return sendKey(subfunction, key);
}

SecurityAccessResult SecurityAccessStateMachine::requestSeed(
    std::uint8_t subfunction) {
    if (!isSeedSubfunction(subfunction)) {
        return negative(subfunction, Nrc::InvalidSubFunction);
    }

    const auto level = levels_.find(subfunction);
    if (level == levels_.end()) {
        return negative(subfunction, Nrc::InvalidSubFunction);
    }

    refreshLockout();
    if (lockoutActive_) {
        return negative(subfunction, Nrc::RequiredTimeDelayNotExpired);
    }

    auto& state = level->second;
    state.seedPending = false;
    state.pendingSeed.clear();

    if (state.unlocked) {
        return positive(subfunction, Seed(state.seedLength, 0));
    }

    Seed seed = sourceFor(state)();
    if (seed.empty()) {
        throw std::runtime_error("the seed source returned an empty seed");
    }

    state.seedLength = seed.size();
    state.pendingSeed = seed;
    state.seedPending = true;
    return positive(subfunction, std::move(seed));
}

SecurityAccessResult SecurityAccessStateMachine::sendKey(
    std::uint8_t subfunction, const Key& key) {
    if (isSeedSubfunction(subfunction) || subfunction == 0) {
        return negative(subfunction, Nrc::InvalidSubFunction);
    }

    const auto level = levels_.find(normalizeLevel(subfunction));
    if (level == levels_.end()) {
        return negative(subfunction, Nrc::InvalidSubFunction);
    }

    refreshLockout();
    if (lockoutActive_) {
        return negative(subfunction, Nrc::ExceededNumberOfAttempts);
    }

    auto& state = level->second;
    if (!state.seedPending) {
        return negative(subfunction, Nrc::RequestSequenceError);
    }

    const Seed seed = std::exchange(state.pendingSeed, {});
    state.seedPending = false;

    if (state.verifier(seed, key)) {
        state.unlocked = true;
        failedAttempts_ = 0;
        return positive(subfunction);
    }

    ++failedAttempts_;
    if (failedAttempts_ >= kMaxSecurityAccessFailures) {
        lockoutActive_ = true;
        lockoutUntil_ = now() + kSecurityAccessLockout;
        return negative(subfunction, Nrc::ExceededNumberOfAttempts);
    }
    return negative(subfunction, Nrc::InvalidKey);
}

bool SecurityAccessStateMachine::isUnlocked(
    std::uint8_t levelOrSubfunction) const {
    const auto level = levels_.find(normalizeLevel(levelOrSubfunction));
    return level != levels_.end() && level->second.unlocked;
}

bool SecurityAccessStateMachine::isLockedOut() const {
    return lockoutActive_ && now() < lockoutUntil_;
}

void SecurityAccessStateMachine::onSessionChange(std::uint8_t newSession) {
    if (currentSession_ == newSession) {
        return;
    }
    currentSession_ = newSession;
    lockAllLevels();
}

void SecurityAccessStateMachine::onSessionTimeout() {
    lockAllLevels();
}

std::uint8_t SecurityAccessStateMachine::normalizeLevel(
    std::uint8_t subfunction) {
    if (isSeedSubfunction(subfunction) || subfunction == 0) {
        return subfunction;
    }
    return static_cast<std::uint8_t>(subfunction - 1);
}

bool SecurityAccessStateMachine::isSeedSubfunction(
    std::uint8_t subfunction) {
    return subfunction != 0 && (subfunction & 0x01U) != 0;
}

SecurityAccessResult SecurityAccessStateMachine::positive(
    std::uint8_t subfunction, Seed seed) {
    SecurityAccessResult result;
    result.subfunction = subfunction;
    result.positive = true;
    result.seed = std::move(seed);
    return result;
}

SecurityAccessResult SecurityAccessStateMachine::negative(
    std::uint8_t subfunction, Nrc nrc) {
    SecurityAccessResult result;
    result.subfunction = subfunction;
    result.nrc = nrc;
    return result;
}

SecurityAccessStateMachine::SeedSource SecurityAccessStateMachine::sourceFor(
    const LevelState& level) const {
    return level.seedSource ? level.seedSource : defaultSeedSource_;
}

void SecurityAccessStateMachine::refreshLockout() {
    if (lockoutActive_ && now() >= lockoutUntil_) {
        lockoutActive_ = false;
        failedAttempts_ = 0;
    }
}

std::chrono::steady_clock::time_point SecurityAccessStateMachine::now() const {
    return clock_ ? clock_() : std::chrono::steady_clock::now();
}

void SecurityAccessStateMachine::lockAllLevels() {
    for (auto& entry : levels_) {
        auto& level = entry.second;
        level.pendingSeed.clear();
        level.seedPending = false;
        level.unlocked = false;
    }
}

}
