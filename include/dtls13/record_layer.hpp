// SPDX-License-Identifier: MIT
// Record-layer orchestration: datagram parsing, sequence reconstruction,
// per-epoch replay protection, AEAD decryption and cross-epoch plaintext
// filtering.

#ifndef DTLS13_RECORD_LAYER_HPP
#define DTLS13_RECORD_LAYER_HPP

#include <cstdint>
#include <memory>
#include <unordered_map>
#include <vector>

#include "dtls13/crypto.hpp"
#include "dtls13/record.hpp"
#include "dtls13/types.hpp"

namespace dtls13 {

// Keying material used to protect records of one epoch.
struct EpochKeys {
    std::unique_ptr<IAead> aead;
    std::unique_ptr<INonceMasker> masker;
};

struct RecordLayerConfig {
    // Maximum number of distinct epochs whose replay windows are kept.
    std::size_t max_tracked_epochs = 8;
    // Maximum accepted ciphertext body size. Protects memory on hostile
    // datagrams regardless of the declared L field.
    std::size_t max_record_body = 1u << 16;
};

// One decrypted / validated record handed to the embedding host.
struct ReceivedRecord {
    std::uint16_t epoch = 0;
    std::uint64_t seq48 = 0;
    ContentType content_type = ContentType::Invalid;
    // Decrypted payload for protected records, or the raw body for
    // plaintext records. Owned by this struct.
    Bytes payload;
    bool was_encrypted = false;
};

struct IngestResult {
    ErrorCode code = ErrorCode::Ok;
    // All records that were fully validated. On the first hard error,
    // processing of the datagram stops and previously accepted records of
    // the same datagram remain available here.
    std::vector<ReceivedRecord> records;
};

class RecordLayer {
public:
    RecordLayer() = default;
    explicit RecordLayer(RecordLayerConfig config) : config_(config) {}

    // Install receive keying material for an epoch. The layer takes
    // ownership of the primitives.
    ErrorCode installEpochKeys(std::uint16_t epoch,
                               std::unique_ptr<IAead> aead,
                               std::unique_ptr<INonceMasker> masker);

    bool hasEpochKeys(std::uint16_t epoch) const {
        return keys_.find(epoch) != keys_.end();
    }

    // Process a complete UDP datagram.
    IngestResult ingest(ByteView datagram);

    // Process a single pre-parsed record. Exposed so hosts that parse
    // datagrams themselves can reuse the exact same security pipeline.
    ErrorCode processRecord(const ParsedRecord& record,
                            ReceivedRecord& out);

    // RFC 9147 section 4.5: a plaintext record whose epoch is older than
    // the highest epoch observed on plaintext records is discarded.
    // Hosts driving a full handshake call this when the epoch advances.
    void notePlaintextEpoch(std::uint16_t epoch) noexcept;

    std::uint16_t currentEpoch() const noexcept { return scanner_.currentEpoch(); }
    const EpochReplayTable& replayTable() const noexcept { return replay_; }

private:
    ErrorCode processPlaintext(const ParsedRecord& record,
                               std::uint16_t epoch,
                               std::uint64_t seq48,
                               ReceivedRecord& out);
    ErrorCode processProtected(const ParsedRecord& record,
                               std::uint16_t epoch,
                               std::uint64_t seq48,
                               ReceivedRecord& out);

    RecordLayerConfig config_;
    RecordScanner scanner_;
    EpochReplayTable replay_{config_.max_tracked_epochs};
    std::unordered_map<std::uint16_t, EpochKeys> keys_;
    std::uint16_t highest_plaintext_epoch_ = 0;
    bool seen_plaintext_ = false;
};

// ---------------------------------------------------------------------------
// Encoding helper for tests and embedding hosts.
// ---------------------------------------------------------------------------

struct EncodeOptions {
    std::uint16_t epoch = 0;
    bool include_epoch = true;
    std::uint64_t seq48 = 0;
    bool use_16bit_seq = false;
    bool include_length = true;
    std::uint16_t body_length = 0;  // Serialized when include_length is set.
    ByteView cid;  // Empty unless a Connection ID is negotiated.
};

// Serialize the leading content-type octet plus a Unified Header.
Bytes encodeUnifiedHeader(ContentType type, const EncodeOptions& options);

// Build a complete protected record: header + AEAD(plaintext, aad=header).
// The real inner content type is the final plaintext octet per RFC 8446;
// callers append it to `plaintext_with_inner_type` themselves.
Bytes buildProtectedRecord(IAead& aead,
                           INonceMasker& masker,
                           ContentType outer_type,
                           const EncodeOptions& options,
                           ByteView plaintext_with_inner_type);

}  // namespace dtls13

#endif  // DTLS13_RECORD_LAYER_HPP
