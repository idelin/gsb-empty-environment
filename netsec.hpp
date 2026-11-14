#pragma once

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <deque>
#include <optional>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace netsec {

using Clock = std::chrono::steady_clock;
using TimePoint = Clock::time_point;

struct Header {
  std::string name;
  std::string value;
};

enum class ErrorCode {
  none = 0,
  connection_closed,
  connection_not_reusable,
  invalid_argument,
  pipeline_depth_exceeded,
  request_timeout,
  response_timeout,
  backend_error_limit,
  early_close,
  unexpected_response,
  out_of_order_response,
  malformed_response,
  header_too_large,
  body_too_large,
  framing_ambiguous,
  unsupported_framing,
  method_mismatch,
  status_mismatch,
  length_mismatch,
};

std::string_view error_code_name(ErrorCode code) noexcept;

struct Failure {
  ErrorCode code = ErrorCode::none;
  bool fatal = false;
  std::string detail;

  explicit operator bool() const noexcept { return code != ErrorCode::none; }
};

struct Request {
  // A value of 0 asks the pairer to allocate a unique id automatically.
  std::uint64_t id = 0;
  std::string method = "GET";
  std::string target;
  std::string version = "HTTP/1.1";

  // Optional application-level checks. When unset, only HTTP semantics are
  // enforced.
  std::optional<std::uint16_t> expected_status;
  std::string response_token;
};

struct Response {
  std::uint16_t status = 0;
  std::string reason;
  std::string version = "HTTP/1.1";
  std::vector<Header> headers;

  // Decoded message body. For the structured entry point a complete object
  // without a declared length is treated as length-delimited by body.size().
  std::string body;

  // These fields make the structured entry point usable without duplicating
  // them in headers. If both are supplied they must agree.
  std::optional<std::uint64_t> content_length;
  bool chunked = false;
  bool close_delimited = false;
  bool connection_close = false;

  // Optional tag used to detect responses that were associated with the
  // wrong request before they reached the pairer.
  std::optional<std::uint64_t> request_id;

  const std::string* find_header(std::string_view name) const noexcept;
};

struct Delivery {
  Request request;
  Response response;
};

struct Result {
  std::vector<Delivery> deliveries;
  Failure failure;

  bool ok() const noexcept { return failure.code == ErrorCode::none; }
  explicit operator bool() const noexcept { return ok(); }
};

// One instance represents exactly one backend HTTP/1.1 connection. It is not
// thread-safe; the owning event loop serialises calls.
class ConnectionPairer {
 public:
  struct Config {
    std::size_t max_pipeline_depth = 32;
    std::size_t max_interim_responses = 8;
    std::size_t max_header_bytes = 64 * 1024;
    std::size_t max_header_count = 256;
    std::size_t max_body_bytes = 8 * 1024 * 1024;
    std::size_t max_frame_overhead_bytes = 256 * 1024;
    std::size_t max_backend_errors = 1;
    std::chrono::milliseconds request_timeout{30'000};
    std::chrono::milliseconds response_timeout{30'000};

    // A close-delimited response can only be confirmed at EOF. It is accepted
    // only when it is the final outstanding request.
    bool allow_close_delimited = true;

    // Require enqueue() to be preceded by prepare_reuse() after an exchange
    // settles. This makes it impossible to accidentally carry state across
    // users of a pooled connection.
    bool require_explicit_reuse = false;

    // Optional backend echo header. When set, every final response must carry
    // this header with the request's response_token (or decimal id). This
    // turns positional pairing into logical pairing when the backend/proxy
    // infrastructure can supply the echo.
    std::string response_id_header;
  };

  ConnectionPairer();
  explicit ConnectionPairer(Config config);
  ~ConnectionPairer() = default;

  ConnectionPairer(const ConnectionPairer&) = delete;
  ConnectionPairer& operator=(const ConnectionPairer&) = delete;
  ConnectionPairer(ConnectionPairer&&) noexcept = default;
  ConnectionPairer& operator=(ConnectionPairer&&) noexcept = default;

  // Queue one request. The caller must not write the request to the backend
  // unless this returns a successful Result.
  Result enqueue(Request request, TimePoint now = Clock::now());
  Result enqueue_request(Request request, TimePoint now = Clock::now()) {
    return enqueue(std::move(request), now);
  }
  Result push_request(Request request, TimePoint now = Clock::now()) {
    return enqueue(std::move(request), now);
  }
  Result submit_request(Request request, TimePoint now = Clock::now()) {
    return enqueue(std::move(request), now);
  }

  // Structured response entry point. If request_id is supplied (or carried by
  // response.request_id) it must match the head of the FIFO exactly. A wrong
  // id is an out-of-order response and poisons the connection.
  Result on_response(Response response, TimePoint now = Clock::now());
  Result on_response(std::uint64_t request_id, Response response,
                     TimePoint now = Clock::now());
  Result on_response(Response response, std::uint64_t request_id,
                     TimePoint now = Clock::now()) {
    return on_response(request_id, std::move(response), now);
  }

  // Raw response bytes. Bytes may arrive split at any point. Only complete,
  // exactly framed responses are returned as deliveries.
  Result feed(std::string_view bytes, TimePoint now = Clock::now());
  Result feed_response(std::string_view bytes,
                       TimePoint now = Clock::now()) {
    return feed(bytes, now);
  }
  Result on_response_bytes(std::string_view bytes,
                           TimePoint now = Clock::now()) {
    return feed(bytes, now);
  }

  // Signal backend EOF. A response body terminated by connection close is
  // completed here; a truncated response or unanswered queued requests abort
  // the connection.
  Result end_of_stream(TimePoint now = Clock::now());
  Result eof(TimePoint now = Clock::now()) { return end_of_stream(now); }

  // Check pending deadlines without supplying data.
  Result poll(TimePoint now = Clock::now());

  // Report a backend I/O/protocol error. Any error while the response stream
  // is active is immediately fatal; idle errors are counted up to the bound.
  Result on_backend_error(std::string detail = {},
                          TimePoint now = Clock::now());

  // Reset an idle connection for its next owner. This succeeds only if no
  // request, partial response, buffered byte, or close indication remains.
  // Otherwise the connection is closed and the call fails.
  Result prepare_reuse(TimePoint now = Clock::now());
  Result reset_for_reuse(TimePoint now = Clock::now()) {
    return prepare_reuse(now);
  }
  Result clear_for_reuse(TimePoint now = Clock::now()) {
    return prepare_reuse(now);
  }
  Result reuse(TimePoint now = Clock::now()) { return prepare_reuse(now); }

  // Deliberately discard the connection and all state without a failure.
  void close() noexcept;

  bool closed() const noexcept { return closed_; }
  bool idle() const noexcept;
  bool reusable() const noexcept;
  bool can_reuse() const noexcept { return reusable(); }
  std::size_t pending_requests() const noexcept { return queue_.size(); }
  std::uint64_t generation() const noexcept { return generation_; }
  const Config& config() const noexcept { return config_; }
  const Failure& last_failure() const noexcept { return last_failure_; }

 private:
  struct PendingRequest {
    Request request;
    TimePoint queued_at;
  };

  enum class ParseState {
    head,
    identity_body,
    chunk_size,
    chunk_data,
    chunk_data_crlf,
    trailers,
    close_body,
  };

  struct Framing {
    bool bodyless = false;
    bool chunked = false;
    bool has_content_length = false;
    std::uint64_t content_length = 0;
    bool connection_close = false;
    bool unknown_length = false;
  };

  struct HeadAnalysis {
    Framing framing;
    Response response;
  };

  Result fail(ErrorCode code, std::string detail, bool fatal = true,
              Result partial = {});
  Result process_input(TimePoint now, Result partial);
  Result process_head(TimePoint now, Result partial);
  Result process_identity(TimePoint now, Result partial);
  Result process_chunk_size(TimePoint now, Result partial);
  Result process_chunk_data(TimePoint now, Result partial);
  Result process_chunk_crlf(TimePoint now, Result partial);
  Result process_trailers(TimePoint now, Result partial);
  Result process_close_body(Result partial);
  Result complete_current(TimePoint now, Result partial,
                          bool end_of_stream = false);

  bool analyze_response_head(Response response, const PendingRequest& pending,
                             HeadAnalysis& analysis, Failure& failure,
                             bool raw_stream);
  bool validate_body_and_pair(const Request& request,
                              const HeadAnalysis& analysis,
                              const Response& response, Failure& failure,
                              std::size_t outstanding) const;

  bool queue_id_exists(std::uint64_t id) const noexcept;
  std::uint64_t allocate_id() noexcept;
  std::size_t available() const noexcept {
    return buffer_.size() - buffer_offset_;
  }
  void consume(std::size_t bytes) noexcept;
  void reset_parser() noexcept;
  void clear_all_state() noexcept;
  void poison(ErrorCode code, std::string detail) noexcept;

  Config config_;
  std::deque<PendingRequest> queue_;
  bool closed_ = false;
  bool backend_will_close_ = false;
  bool needs_reuse_ = false;
  bool response_head_seen_ = false;
  bool current_in_progress_ = false;
  std::size_t backend_errors_ = 0;
  std::size_t interim_responses_ = 0;
  std::uint64_t generation_ = 0;
  std::uint64_t next_auto_id_ = 1;
  Failure last_failure_;

  std::string buffer_;
  std::size_t buffer_offset_ = 0;
  ParseState parse_state_ = ParseState::head;
  Response current_;
  Framing current_framing_;
  std::uint64_t remaining_ = 0;
  std::uint64_t current_body_bytes_ = 0;
  std::size_t trailer_bytes_ = 0;
  std::size_t frame_overhead_bytes_ = 0;
  TimePoint response_started_at_{};
};

// Descriptive aliases for callers that model the same object by role.
using ResponsePairer = ConnectionPairer;
using PipelinePairer = ConnectionPairer;

}  // namespace netsec
