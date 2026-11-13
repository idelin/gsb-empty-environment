#pragma once

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <map>
#include <vector>

namespace netsec {

inline constexpr std::uint8_t kSecurityAccessService = 0x27;
inline constexpr std::chrono::seconds kSecurityAccessLockout{10};
inline constexpr std::uint8_t kMaxSecurityAccessFailures = 3;

enum class Nrc : std::uint8_t {
    None = 0x00,
    InvalidSubFunction = 0x12,
    RequestSequenceError = 0x24,
    InvalidKey = 0x35,
    ExceededNumberOfAttempts = 0x36,
    RequiredTimeDelayNotExpired = 0x37,
};

using SecurityAccessNrc = Nrc;
using Seed = std::vector<std::uint8_t>;
using Key = Seed;

struct SecurityAccessResult {
    std::uint8_t subfunction = 0;
    bool positive = false;
    Seed seed;
    Nrc nrc = Nrc::None;

    bool succeeded() const { return positive; }
    bool failed() const { return !positive; }
    explicit operator bool() const { return positive; }
};

class SecurityAccessStateMachine {
public:
    using SeedSource = std::function<Seed()>;
    using KeyVerifier = std::function<bool(const Seed& seed, const Key& key)>;
    using Clock = std::function<std::chrono::steady_clock::time_point()>;

    SecurityAccessStateMachine();
    explicit SecurityAccessStateMachine(SeedSource seedSource,
                                        Clock clock = {},
                                        std::uint8_t initialSession = 0x01);

    void registerLevel(std::uint8_t requestSeedSubfunction,
                       KeyVerifier verifier,
                       SeedSource seedSource = {});
    void registerLevel(std::uint8_t requestSeedSubfunction,
                       Key expectedKey,
                       SeedSource seedSource = {});

    SecurityAccessResult handle(std::uint8_t subfunction,
                                const Key& key = {});
    SecurityAccessResult handleRequest(std::uint8_t subfunction,
                                       const Key& key = {}) {
        return handle(subfunction, key);
    }
    SecurityAccessResult dispatch(std::uint8_t subfunction,
                                  const Key& key = {}) {
        return handle(subfunction, key);
    }

    SecurityAccessResult requestSeed(std::uint8_t subfunction);
    SecurityAccessResult sendKey(std::uint8_t subfunction, const Key& key);

    bool isUnlocked(std::uint8_t levelOrSubfunction) const;
    bool isLevelUnlocked(std::uint8_t levelOrSubfunction) const {
        return isUnlocked(levelOrSubfunction);
    }
    bool isLockedOut() const;
    std::uint8_t failedAttempts() const { return failedAttempts_; }

    void onSessionChange(std::uint8_t newSession);
    void changeSession(std::uint8_t newSession) {
        onSessionChange(newSession);
    }
    void onSessionTimeout();
    void sessionTimeout() { onSessionTimeout(); }
    std::uint8_t currentSession() const { return currentSession_; }

private:
    struct LevelState {
        KeyVerifier verifier;
        SeedSource seedSource;
        Seed pendingSeed;
        std::size_t seedLength = 4;
        bool seedPending = false;
        bool unlocked = false;
    };

    static std::uint8_t normalizeLevel(std::uint8_t subfunction);
    static bool isSeedSubfunction(std::uint8_t subfunction);
    static SecurityAccessResult positive(std::uint8_t subfunction, Seed seed = {});
    static SecurityAccessResult negative(std::uint8_t subfunction, Nrc nrc);

    SeedSource sourceFor(const LevelState& level) const;
    void refreshLockout();
    std::chrono::steady_clock::time_point now() const;
    void lockAllLevels();

    SeedSource defaultSeedSource_;
    Clock clock_;
    std::map<std::uint8_t, LevelState> levels_;
    std::chrono::steady_clock::time_point lockoutUntil_{};
    bool lockoutActive_ = false;
    std::uint8_t failedAttempts_ = 0;
    std::uint8_t currentSession_ = 0x01;
};

using SecurityAccess = SecurityAccessStateMachine;

}
