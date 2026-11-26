// SPDX-License-Identifier: MIT

#include "dtls13/record_layer.hpp"

#include <limits>

namespace dtls13 {

namespace {

bool isPlaintextType(ContentType type) noexcept {
    // Plaintext handshake / ACK / CCS are the only records that travel
    // without AEAD protection in DTLS 1.3.
    return type == ContentType::Handshake ||
           type == ContentType::ChangeCipherSpec ||
           type == ContentType::Alert ||
           type == ContentType::ApplicationData;
}

}  // namespace

ErrorCode RecordLayer::installEpochKeys(
    std::uint16_t epoch,
    std::unique_ptr<IAead> aead,
    std::unique_ptr<INonceMasker> masker) {
    if (!aead || !masker) {
        return ErrorCode::BadArgument;
    }
    EpochKeys keys;
    keys.aead = std::move(aead);
    keys.masker = std::move(masker);
    keys_[epoch] = std::move(keys);
    return ErrorCode::Ok;
}

void RecordLayer::notePlaintextEpoch(std::uint16_t epoch) noexcept {
    if (!seen_plaintext_ || epoch > highest_plaintext_epoch_) {
        seen_plaintext_ = true;
        highest_plaintext_epoch_ = epoch;
    }
}

IngestResult RecordLayer::ingest(ByteView datagram) {
    IngestResult output;
    std::size_t offset = 0;
    while (offset < datagram.size()) {
        ParsedRecord record;
        ErrorCode parse_error =
            scanner_.next(datagram, offset, record);
        if (parse_error != ErrorCode::Ok) {
            output.code = parse_error;
            return output;
        }

        ReceivedRecord received;
        parse_error = processRecord(record, received);
        if (parse_error != ErrorCode::Ok) {
            output.code = parse_error;
            return output;
        }
        output.records.push_back(std::move(received));
    }
    output.code = ErrorCode::Ok;
    return output;
}

ErrorCode RecordLayer::processRecord(const ParsedRecord& record,
                                     ReceivedRecord& out) {
    const UnifiedHeader& header = record.header;
    const std::uint16_t epoch = header.epoch;

    if (record.body.size() > config_.max_record_body) {
        return ErrorCode::MalformedRecord;
    }

    const bool encrypted = hasEpochKeys(epoch);
    if (!encrypted) {
        // Drop stale plaintext epochs before allocating any replay state,
        // so hostile traffic cannot grow the epoch table.
        if (seen_plaintext_ && epoch < highest_plaintext_epoch_) {
            return ErrorCode::StaleEpoch;
        }
        if (!isPlaintextType(record.outer_type)) {
            return ErrorCode::MalformedRecord;
        }
    }

    ReplayWindow* window = nullptr;
    ErrorCode table_error = replay_.getOrCreate(epoch, window);
    if (table_error != ErrorCode::Ok) {
        return table_error;
    }

    const std::uint64_t seq48 = reconstructSequence(
        header.truncated_seq, header.seq_bits, window->windowEnd());

    if (encrypted) {
        return processProtected(record, epoch, seq48, out);
    }
    return processPlaintext(record, epoch, seq48, out);
}

ErrorCode RecordLayer::processPlaintext(const ParsedRecord& record,
                                        std::uint16_t epoch,
                                        std::uint64_t seq48,
                                        ReceivedRecord& out) {
    ReplayWindow* window = replay_.find(epoch);
    if (window == nullptr) {
        return ErrorCode::UnknownEpoch;
    }
    if (window->inspect(seq48) == ReplayWindow::Verdict::Replayed) {
        return ErrorCode::Replayed;
    }

    // Plaintext records have no AEAD stage; structural validation above is
    // the authentication boundary, so committing here is safe and still
    // keeps the window untouched for rejected / duplicate records.
    window->commit(seq48);
    notePlaintextEpoch(epoch);

    out.epoch = epoch;
    out.seq48 = seq48;
    out.content_type = record.outer_type;
    out.was_encrypted = false;
    out.payload.assign(record.body.data(),
                       record.body.data() + record.body.size());
    return ErrorCode::Ok;
}

ErrorCode RecordLayer::processProtected(const ParsedRecord& record,
                                        std::uint16_t epoch,
                                        std::uint64_t seq48,
                                        ReceivedRecord& out) {
    ReplayWindow* window = replay_.find(epoch);
    if (window == nullptr) {
        return ErrorCode::UnknownEpoch;
    }

    // 1. Replay gate before any cryptographic work. inspect() is const:
    // failed authentication never advances the window.
    const ReplayWindow::Verdict verdict = window->inspect(seq48);
    if (verdict == ReplayWindow::Verdict::Replayed) {
        return ErrorCode::Replayed;
    }
    if (verdict == ReplayWindow::Verdict::TooOld) {
        return ErrorCode::Replayed;
    }

    EpochKeys& keys = keys_.at(epoch);
    std::vector<std::uint8_t> nonce(keys.masker->nonceLength(), 0);
    keys.masker->mask(seq48, nonce.data());

    // 2. AEAD over the serialized record header as AAD (RFC 9147 4.5.2).
    auto plaintext = keys.aead->open(
        ByteView(nonce), record.body, record.raw_header);
    if (!plaintext.ok()) {
        // Explicitly do not touch the replay window here.
        return plaintext.code;
    }

    // 3. Authentication succeeded: this is the only commit point.
    window->commit(seq48);

    if (plaintext.value.empty()) {
        return ErrorCode::MalformedRecord;
    }
    // TLS 1.3 inner content type is the final plaintext octet.
    const std::uint8_t inner_type = plaintext.value.back();
    plaintext.value.pop_back();

    out.epoch = epoch;
    out.seq48 = seq48;
    out.content_type = static_cast<ContentType>(inner_type);
    out.was_encrypted = true;
    out.payload = std::move(plaintext.value);
    return ErrorCode::Ok;
}

Bytes encodeUnifiedHeader(ContentType type, const EncodeOptions& options) {
    Bytes header;
    header.push_back(static_cast<std::uint8_t>(type));

    std::uint8_t flags = kUhFixedPattern;
    if (!options.cid.empty()) {
        flags |= kUhCidFlag;
    }
    if (options.include_epoch) {
        flags |= kUhEpochFlag;
    }
    if (options.use_16bit_seq) {
        flags |= kUhSeq16Flag;
    }
    if (options.include_length) {
        flags |= kUhLengthFlag;
    }
    header.push_back(flags);

    if (!options.cid.empty()) {
        header.insert(header.end(), options.cid.data(),
                      options.cid.data() + options.cid.size());
    }
    if (options.include_epoch) {
        std::uint8_t epoch_bytes[2];
        writeBe16(epoch_bytes, options.epoch);
        header.insert(header.end(), epoch_bytes, epoch_bytes + 2);
    }

    if (options.use_16bit_seq) {
        std::uint8_t seq_bytes[2];
        writeBe16(seq_bytes,
                  static_cast<std::uint16_t>(options.seq48 & 0xFFFFULL));
        header.insert(header.end(), seq_bytes, seq_bytes + 2);
    } else {
        header.push_back(
            static_cast<std::uint8_t>(options.seq48 & 0xFFULL));
    }
    if (options.include_length) {
        std::uint8_t length_bytes[2];
        writeBe16(length_bytes, options.body_length);
        header.insert(header.end(), length_bytes, length_bytes + 2);
    }
    return header;
}

Bytes buildProtectedRecord(IAead& aead,
                           INonceMasker& masker,
                           ContentType outer_type,
                           const EncodeOptions& options,
                           ByteView plaintext_with_inner_type) {
    std::vector<std::uint8_t> nonce(masker.nonceLength(), 0);
    masker.mask(options.seq48, nonce.data());
    // The length field covers ciphertext + tag, whose size is known ahead
    // of encryption because the reference AEAD has a fixed tag length.
    EncodeOptions header_options = options;
    header_options.body_length = static_cast<std::uint16_t>(
        plaintext_with_inner_type.size() + aead.tagLength());
    Bytes header = encodeUnifiedHeader(outer_type, header_options);
    Bytes sealed = aead.seal(ByteView(nonce), plaintext_with_inner_type,
                             ByteView(header));

    Bytes record = header;
    record.insert(record.end(), sealed.begin(), sealed.end());
    return record;
}

}  // namespace dtls13
