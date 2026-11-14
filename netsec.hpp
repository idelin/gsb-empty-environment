#pragma once

#include <algorithm>
#include <chrono>
#include <compare>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <map>
#include <mutex>
#include <optional>
#include <stdexcept>
#include <string>
#include <string_view>
#include <tuple>
#include <utility>
#include <vector>

namespace netsec {

using Byte = std::uint8_t;
using Bytes = std::vector<Byte>;
using Token = Bytes;
using TimePoint = std::chrono::steady_clock::time_point;

inline Bytes bytes_from_string(std::string_view text) {
    Bytes out;
    out.reserve(text.size());
    for (char ch : text) {
        out.push_back(static_cast<Byte>(static_cast<unsigned char>(ch)));
    }
    return out;
}

inline std::string string_from_bytes(const Bytes& bytes) {
    if (bytes.empty()) {
        return {};
    }
    return std::string(reinterpret_cast<const char*>(bytes.data()), bytes.size());
}

inline Bytes bytes_from_hex(std::string_view hex) {
    auto nibble = [](char ch) -> unsigned {
        if (ch >= '0' && ch <= '9') {
            return static_cast<unsigned>(ch - '0');
        }
        if (ch >= 'a' && ch <= 'f') {
            return static_cast<unsigned>(ch - 'a' + 10);
        }
        if (ch >= 'A' && ch <= 'F') {
            return static_cast<unsigned>(ch - 'A' + 10);
        }
        throw std::invalid_argument("netsec::bytes_from_hex: invalid digit");
    };

    if ((hex.size() % 2U) != 0U) {
        throw std::invalid_argument("netsec::bytes_from_hex: odd number of digits");
    }

    Bytes out;
    out.reserve(hex.size() / 2U);
    for (std::size_t i = 0; i < hex.size(); i += 2U) {
        out.push_back(static_cast<Byte>((nibble(hex[i]) << 4U) | nibble(hex[i + 1U])));
    }
    return out;
}

inline std::string hex_from_bytes(const Bytes& bytes) {
    static constexpr char digits[] = "0123456789abcdef";
    std::string out;
    out.reserve(bytes.size() * 2U);
    for (Byte byte : bytes) {
        out.push_back(digits[(byte >> 4U) & 0x0fU]);
        out.push_back(digits[byte & 0x0fU]);
    }
    return out;
}

// Transport/session identity of the requester.  Both fields are part of the
// cache identity.  A token is never a session identifier.
struct Requester {
    std::string session_id;
    std::string endpoint;

    auto operator<=>(const Requester&) const = default;
};

// Every field of this key is part of the block-transfer identity.  A block
// exchange is only allowed to continue when the complete binding is unchanged.
struct BlockKey {
    Token token;
    std::string method;
    std::string uri;
    std::optional<std::uint32_t> content_format;
    std::uint32_t block_size{0};
    std::uint32_t start_sequence{0};
    Requester requester;

    // Optional metadata for the representation identified by the ETag.  If
    // present, it is enforced at completion.
    std::optional<std::uint64_t> total_length;

    // Optional expected ETag.  If omitted, the first block's ETag is adopted.
    std::optional<Bytes> etag;

    [[nodiscard]] bool same_binding(const BlockKey& other) const {
        return token == other.token && method == other.method && uri == other.uri &&
               content_format == other.content_format &&
               block_size == other.block_size &&
               start_sequence == other.start_sequence &&
               requester == other.requester;
    }

    bool operator==(const BlockKey&) const = default;
};

enum class BlockStatus {
    Accepted,
    Complete,
    Duplicate,
    Stale,
    ConflictingDuplicate,
    SequenceGap,
    ContextMismatch,
    EtagMismatch,
    LengthMismatch,
    InvalidBlockSize,
    InvalidPayload,
    MissingExpectedLength,
    Expired,
    CapacityExceeded,
    NotFound
};

inline std::string_view status_name(BlockStatus status) {
    switch (status) {
        case BlockStatus::Accepted: return "Accepted";
        case BlockStatus::Complete: return "Complete";
        case BlockStatus::Duplicate: return "Duplicate";
        case BlockStatus::Stale: return "Stale";
        case BlockStatus::ConflictingDuplicate: return "ConflictingDuplicate";
        case BlockStatus::SequenceGap: return "SequenceGap";
        case BlockStatus::ContextMismatch: return "ContextMismatch";
        case BlockStatus::EtagMismatch: return "EtagMismatch";
        case BlockStatus::LengthMismatch: return "LengthMismatch";
        case BlockStatus::InvalidBlockSize: return "InvalidBlockSize";
        case BlockStatus::InvalidPayload: return "InvalidPayload";
        case BlockStatus::MissingExpectedLength: return "MissingExpectedLength";
        case BlockStatus::Expired: return "Expired";
        case BlockStatus::CapacityExceeded: return "CapacityExceeded";
        case BlockStatus::NotFound: return "NotFound";
    }
    return "Unknown";
}

struct Block {
    BlockKey key;
    std::uint32_t sequence{0};
    Bytes etag;
    bool more{false};
    Bytes payload;
};

struct BlockResult {
    BlockStatus status{BlockStatus::InvalidPayload};
    std::uint32_t next_sequence{0};
    std::uint64_t received_bytes{0};
    std::optional<std::uint64_t> expected_length;
    Bytes body;
    bool state_reset{false};
    bool restarted{false};

    [[nodiscard]] bool accepted() const {
        return status == BlockStatus::Accepted || status == BlockStatus::Complete;
    }

    [[nodiscard]] bool complete() const {
        return status == BlockStatus::Complete;
    }

    // Duplicate and stale blocks are deliberately discarded, but they are
    // normal, non-fatal outcomes for a CoAP endpoint.
    [[nodiscard]] bool handled() const {
        return accepted() || status == BlockStatus::Duplicate ||
               status == BlockStatus::Stale;
    }
};

struct ObservationKey {
    Requester requester;
    Token token;
    std::string method;
    std::string uri;

    auto operator<=>(const ObservationKey&) const = default;
};

struct BlockTransferOptions {
    std::chrono::milliseconds ttl{std::chrono::minutes(5)};
    std::size_t max_transfers{4096};
    std::size_t max_body_bytes{16U * 1024U * 1024U};

    // When true, completion is refused unless the representation length is
    // known from BlockKey::total_length or register_etag().
    bool require_expected_length{true};

    // false: a changed binding is rejected and state is removed.
    // true:  a changed binding may start a fresh transfer, but only when the
    //        incoming block is the configured start block.
    bool restart_on_context_mismatch{false};

    // Injectable for deterministic expiry tests.
    std::function<TimePoint()> clock;
};

// Assembles inbound block-wise payloads.  State is keyed by session, endpoint
// and token, and every received block is checked against the complete
// BlockKey before its bytes can reach the assembly buffer.
class BlockTransfer {
public:
    explicit BlockTransfer(BlockTransferOptions options = {});

    BlockResult receive(const Block& block);

    // Queries require the complete key.  Looking up assembled bytes by token
    // alone would reintroduce the confused-deputy bug this component prevents.
    [[nodiscard]] bool has_transfer(const BlockKey& key) const;
    [[nodiscard]] bool is_complete(const BlockKey& key) const;
    [[nodiscard]] std::optional<Bytes> body(const BlockKey& key) const;
    [[nodiscard]] std::optional<std::uint32_t> next_sequence(
        const BlockKey& key) const;

    // Removes block-transfer state only.  Observations are a separate plane.
    bool reset(const Requester& requester, const Token& token);
    void reset_all();
    [[nodiscard]] std::size_t active_transfers() const;
    [[nodiscard]] std::size_t sweep_expired();

    // Binds an ETag to a representation length.  This is how a server that
    // knows the resource metadata can enforce "length belongs to this ETag"
    // without repeating the length in every block.
    void register_etag(Bytes etag, std::uint64_t total_length);
    void unregister_etag(const Bytes& etag);
    [[nodiscard]] std::optional<std::uint64_t> etag_length(
        const Bytes& etag) const;

    // Observation relations intentionally have their own storage and lookup
    // key.  Creating/removing an observation never creates/removes block
    // state, and resetting block state never changes observations.
    void observe(const ObservationKey& key);
    bool cancel_observation(const ObservationKey& key);
    [[nodiscard]] bool is_observed(const ObservationKey& key) const;
    [[nodiscard]] std::size_t observation_count() const;
    void clear_observations();

private:
    using SlotKey = std::tuple<std::string, std::string, Token>;

    struct State {
        BlockKey key;
        Bytes etag;
        Bytes body;
        std::uint32_t next_sequence{0};
        bool complete{false};
        std::optional<std::uint64_t> expected_length;
        TimePoint last_activity{};
    };

    struct ObservationInfo {
        TimePoint registered{};
    };

    using StateMap = std::map<SlotKey, State>;
    using StateIterator = StateMap::iterator;
    using ObservationMap = std::map<ObservationKey, ObservationInfo>;

    static SlotKey slot_of(const BlockKey& key);
    TimePoint now_locked() const;
    bool expired_locked(const State& state, TimePoint now) const;
    void sweep_locked(TimePoint now);
    bool evict_oldest_locked();
    std::optional<std::uint64_t> etag_length_locked(const Bytes& etag) const;

    BlockResult start_locked(const Block& block, TimePoint now, bool restarted);
    BlockResult append_locked(StateIterator state, const Block& block,
                              TimePoint now);
    BlockResult discard_locked(StateIterator state, const Block& block,
                               TimePoint now);

    BlockTransferOptions options_;
    std::function<TimePoint()> clock_;
    mutable std::mutex mutex_;
    StateMap states_;
    ObservationMap observations_;
    std::map<Bytes, std::uint64_t> etag_lengths_;
};

// Splits a representation into CoAP block payloads using key.block_size and
// key.start_sequence.  The returned blocks carry the same complete key.
inline std::vector<Block> make_blocks(const BlockKey& key, const Bytes& body,
                                      const Bytes& etag) {
    if (key.block_size == 0U) {
        throw std::invalid_argument("netsec::make_blocks: block_size is zero");
    }

    std::vector<Block> blocks;
    std::uint32_t sequence = key.start_sequence;

    if (body.empty()) {
        Block block;
        block.key = key;
        block.sequence = sequence;
        block.etag = etag;
        block.more = false;
        blocks.push_back(std::move(block));
        return blocks;
    }

    for (std::size_t offset = 0; offset < body.size();
         offset += key.block_size) {
        const std::size_t length =
            std::min<std::size_t>(key.block_size, body.size() - offset);

        Block block;
        block.key = key;
        block.sequence = sequence++;
        block.etag = etag;
        block.more = (offset + length) < body.size();
        block.payload.assign(body.begin() + static_cast<std::ptrdiff_t>(offset),
                             body.begin() +
                                 static_cast<std::ptrdiff_t>(offset + length));
        blocks.push_back(std::move(block));
    }
    return blocks;
}

// Server-side block-wise download representation.
struct Representation {
    Bytes payload;
    Bytes etag;
    std::optional<std::uint32_t> content_format;
};

struct BlockRequest {
    BlockKey key;
    std::uint32_t sequence{0};

    // A client-supplied conditional ETag, if any.
    std::optional<Bytes> etag;
};

struct BlockReply {
    BlockStatus status{BlockStatus::InvalidPayload};
    Bytes payload;
    std::uint32_t sequence{0};
    std::uint32_t block_size{0};
    bool more{false};
    Bytes etag;
    std::size_t offset{0};
    std::uint64_t total_length{0};
    std::uint32_t next_sequence{0};
    bool restarted{false};

    [[nodiscard]] bool served() const {
        return status == BlockStatus::Accepted ||
               status == BlockStatus::Complete ||
               status == BlockStatus::Duplicate;
    }
};

struct BlockServerOptions {
    std::chrono::milliseconds ttl{std::chrono::minutes(5)};
    std::size_t max_transfers{4096};
    bool restart_on_context_mismatch{false};
    std::function<TimePoint()> clock;
};

// Serves Block2 responses from a server-side representation.  The response
// state is bound to the same complete key, so a token reused for another URI
// can never cause bytes from the previous representation to be served.
class BlockServer {
public:
    explicit BlockServer(BlockServerOptions options = {});

    BlockReply serve(const BlockRequest& request,
                     const Representation& representation);

    bool reset(const Requester& requester, const Token& token);
    void reset_all();
    [[nodiscard]] std::size_t active_transfers() const;
    [[nodiscard]] std::size_t sweep_expired();

private:
    using SlotKey = std::tuple<std::string, std::string, Token>;

    struct State {
        BlockKey key;
        Bytes body;
        Bytes etag;
        std::uint32_t next_sequence{0};
        bool complete{false};
        TimePoint last_activity{};
    };

    using StateMap = std::map<SlotKey, State>;
    using StateIterator = StateMap::iterator;

    static SlotKey slot_of(const BlockKey& key);
    TimePoint now_locked() const;
    bool expired_locked(const State& state, TimePoint now) const;
    void sweep_locked(TimePoint now);
    bool evict_oldest_locked();
    BlockReply serve_from_locked(StateIterator state, std::uint32_t sequence,
                                 bool duplicate);
    BlockReply start_and_serve_locked(const BlockRequest& request,
                                      const Representation& representation,
                                      TimePoint now, bool restarted);

    BlockServerOptions options_;
    std::function<TimePoint()> clock_;
    mutable std::mutex mutex_;
    StateMap states_;
};

}  // namespace netsec
