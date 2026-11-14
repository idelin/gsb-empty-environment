#include "netsec.hpp"

#include <algorithm>
#include <cctype>
#include <limits>
#include <utility>

namespace netsec {
namespace {

constexpr std::string_view kCrlf = "\r\n";
constexpr std::string_view kHeadEnd = "\r\n\r\n";

bool is_tchar(unsigned char c) noexcept {
  if (std::isalnum(c) != 0) {
    return true;
  }
  switch (c) {
    case '!':
    case '#':
    case '$':
    case '%':
    case '&':
    case '\'':
    case '*':
    case '+':
    case '-':
    case '.':
    case '^':
    case '_':
    case '`':
    case '|':
    case '~':
      return true;
    default:
      return false;
  }
}

bool valid_token(std::string_view value) noexcept {
  if (value.empty()) {
    return false;
  }
  for (const unsigned char c : value) {
    if (!is_tchar(c)) {
      return false;
    }
  }
  return true;
}

bool valid_header_value(std::string_view value) noexcept {
  for (const unsigned char c : value) {
    if (c == '\t') {
      continue;
    }
    if (c < 0x20 || c == 0x7f) {
      return false;
    }
  }
  return true;
}

std::string_view trim_ows(std::string_view value) noexcept {
  while (!value.empty() && (value.front() == ' ' || value.front() == '\t')) {
    value.remove_prefix(1);
  }
  while (!value.empty() && (value.back() == ' ' || value.back() == '\t')) {
    value.remove_suffix(1);
  }
  return value;
}

bool iequals(std::string_view lhs, std::string_view rhs) noexcept {
  if (lhs.size() != rhs.size()) {
    return false;
  }
  for (std::size_t i = 0; i < lhs.size(); ++i) {
    if (std::tolower(static_cast<unsigned char>(lhs[i])) !=
        std::tolower(static_cast<unsigned char>(rhs[i]))) {
      return false;
    }
  }
  return true;
}

bool parse_decimal(std::string_view value, std::uint64_t& out) noexcept {
  if (value.empty()) {
    return false;
  }
  std::uint64_t result = 0;
  for (const unsigned char c : value) {
    if (c < '0' || c > '9') {
      return false;
    }
    const std::uint64_t digit = c - '0';
    if (result > (std::numeric_limits<std::uint64_t>::max() - digit) / 10) {
      return false;
    }
    result = result * 10 + digit;
  }
  out = result;
  return true;
}

int hex_value(unsigned char c) noexcept {
  if (c >= '0' && c <= '9') {
    return c - '0';
  }
  if (c >= 'a' && c <= 'f') {
    return c - 'a' + 10;
  }
  if (c >= 'A' && c <= 'F') {
    return c - 'A' + 10;
  }
  return -1;
}

bool parse_hex(std::string_view value, std::uint64_t& out) noexcept {
  if (value.empty()) {
    return false;
  }
  std::uint64_t result = 0;
  for (const unsigned char c : value) {
    const int digit = hex_value(c);
    if (digit < 0) {
      return false;
    }
    if (result > (std::numeric_limits<std::uint64_t>::max() -
                  static_cast<std::uint64_t>(digit)) /
                     16) {
      return false;
    }
    result = result * 16 + static_cast<std::uint64_t>(digit);
  }
  out = result;
  return true;
}

bool parse_http_version(std::string_view value) noexcept {
  return value == "HTTP/1.0" || value == "HTTP/1.1";
}

bool valid_reason(std::string_view reason) noexcept {
  for (const unsigned char c : reason) {
    if (c == '\r' || c == '\n' || c == '\0') {
      return false;
    }
  }
  return true;
}

struct ParsedHead {
  std::uint16_t status = 0;
  std::string reason;
  std::string version;
  std::vector<Header> headers;
};

bool parse_status_line(std::string_view line, ParsedHead& out,
                       std::string& error) {
  const std::size_t first_space = line.find(' ');
  if (first_space == std::string_view::npos) {
    error = "status line has no separator";
    return false;
  }
  const std::string_view version = line.substr(0, first_space);
  if (!parse_http_version(version)) {
    error = "unsupported HTTP version in status line";
    return false;
  }

  std::string_view rest = line.substr(first_space + 1);
  while (!rest.empty() && rest.front() == ' ') {
    rest.remove_prefix(1);
  }
  if (rest.size() < 3) {
    error = "status code is too short";
    return false;
  }
  if (rest[0] < '1' || rest[0] > '5' || rest[1] < '0' || rest[1] > '9' ||
      rest[2] < '0' || rest[2] > '9') {
    error = "status code is not three digits";
    return false;
  }
  if (rest.size() > 3 && rest[3] != ' ') {
    error = "status code is not followed by a separator";
    return false;
  }
  const std::uint16_t status = static_cast<std::uint16_t>(
      (rest[0] - '0') * 100 + (rest[1] - '0') * 10 + (rest[2] - '0'));
  if (status < 100 || status > 599) {
    error = "status code out of range";
    return false;
  }
  std::string_view reason;
  if (rest.size() > 4) {
    reason = rest.substr(4);
  } else if (rest.size() == 4) {
    reason = {};
  }
  if (!valid_reason(reason)) {
    error = "invalid character in reason phrase";
    return false;
  }

  out.status = status;
  out.version = std::string(version);
  out.reason = std::string(reason);
  return true;
}

bool parse_header_line(std::string_view line, Header& out,
                       std::string& error) {
  if (line.empty()) {
    error = "empty header line";
    return false;
  }
  if (line.front() == ' ' || line.front() == '\t') {
    error = "obsolete header folding is not accepted";
    return false;
  }
  const std::size_t colon = line.find(':');
  if (colon == std::string_view::npos) {
    error = "header line has no colon";
    return false;
  }
  const std::string_view name = line.substr(0, colon);
  if (!valid_token(name)) {
    error = "invalid header field name";
    return false;
  }
  const std::string_view value = trim_ows(line.substr(colon + 1));
  if (!valid_header_value(value)) {
    error = "invalid header field value";
    return false;
  }
  out.name = std::string(name);
  out.value = std::string(value);
  return true;
}

bool parse_head_block(std::string_view block, ParsedHead& out,
                      std::size_t max_header_count, std::string& error) {
  const std::size_t line_end = block.find(kCrlf);
  std::string_view status_line = block;
  std::string_view rest;
  if (line_end != std::string_view::npos) {
    status_line = block.substr(0, line_end);
    rest = block.substr(line_end + kCrlf.size());
  }
  if (!parse_status_line(status_line, out, error)) {
    return false;
  }

  while (!rest.empty()) {
    if (out.headers.size() >= max_header_count) {
      error = "too many header fields";
      return false;
    }
    const std::size_t end = rest.find(kCrlf);
    std::string_view line = rest;
    if (end == std::string_view::npos) {
      rest = {};
    } else {
      line = rest.substr(0, end);
      rest.remove_prefix(end + kCrlf.size());
    }
    if (line.empty()) {
      break;
    }
    Header header;
    if (!parse_header_line(line, header, error)) {
      return false;
    }
    out.headers.push_back(std::move(header));
  }
  return true;
}

std::vector<std::string> split_comma_list(std::string_view value) {
  std::vector<std::string> result;
  while (true) {
    const std::size_t comma = value.find(',');
    const std::string_view part =
        comma == std::string_view::npos ? value : value.substr(0, comma);
    result.push_back(std::string(trim_ows(part)));
    if (comma == std::string_view::npos) {
      break;
    }
    value.remove_prefix(comma + 1);
  }
  return result;
}

bool has_connection_token(const std::vector<Header>& headers,
                          std::string_view token) {
  for (const Header& header : headers) {
    if (!iequals(header.name, "connection")) {
      continue;
    }
    for (const std::string& part : split_comma_list(header.value)) {
      if (iequals(part, token)) {
        return true;
      }
    }
  }
  return false;
}

enum class FramingError {
  none,
  malformed,
  ambiguous,
  unsupported,
};

struct HeaderFraming {
  bool chunked = false;
  bool has_content_length = false;
  std::uint64_t content_length = 0;
  bool connection_close = false;
};

HeaderFraming inspect_framing_headers(const ParsedHead& head,
                                      const Response& response,
                                      FramingError& error,
                                      std::string& detail) {
  HeaderFraming result;
  bool saw_transfer_encoding = false;
  bool saw_content_length = false;
  std::uint64_t content_length = 0;

  for (const Header& header : head.headers) {
    if (iequals(header.name, "transfer-encoding")) {
      saw_transfer_encoding = true;
      const std::vector<std::string> codings =
          split_comma_list(header.value);
      if (codings.size() != 1 || codings.front().empty()) {
        error = FramingError::unsupported;
        detail = "only a single chunked transfer coding is supported";
        return result;
      }
      if (!iequals(codings.front(), "chunked")) {
        error = FramingError::unsupported;
        detail = "unsupported transfer coding";
        return result;
      }
      result.chunked = true;
    } else if (iequals(header.name, "content-length")) {
      if (header.value.find(',') != std::string::npos) {
        error = FramingError::malformed;
        detail = "comma-separated Content-Length is not accepted";
        return result;
      }
      std::uint64_t parsed = 0;
      if (!parse_decimal(trim_ows(header.value), parsed)) {
        error = FramingError::malformed;
        detail = "invalid Content-Length";
        return result;
      }
      if (saw_content_length && parsed != content_length) {
        error = FramingError::malformed;
        detail = "conflicting Content-Length values";
        return result;
      }
      saw_content_length = true;
      content_length = parsed;
    }
  }

  if (response.content_length.has_value()) {
    if (saw_content_length && *response.content_length != content_length) {
      error = FramingError::malformed;
      detail = "Content-Length field disagrees with header";
      return result;
    }
    saw_content_length = true;
    content_length = *response.content_length;
  }
  if (response.chunked) {
    result.chunked = true;
  }

  if (result.chunked && saw_content_length) {
    error = FramingError::ambiguous;
    detail = "both Transfer-Encoding and Content-Length are present";
    return result;
  }
  if (saw_transfer_encoding && response.content_length.has_value()) {
    error = FramingError::ambiguous;
    detail = "Transfer-Encoding with an explicit Content-Length";
    return result;
  }

  result.has_content_length = saw_content_length;
  result.content_length = content_length;
  result.connection_close =
      response.connection_close || has_connection_token(head.headers, "close");
  if (head.version == "HTTP/1.0" &&
      !has_connection_token(head.headers, "keep-alive")) {
    result.connection_close = true;
  }
  return result;
}

bool is_interim_status(std::uint16_t status) noexcept {
  return status >= 100 && status <= 199;
}

std::string expected_binding(const Request& request) {
  if (!request.response_token.empty()) {
    return request.response_token;
  }
  return std::to_string(request.id);
}

}  // namespace

std::string_view error_code_name(ErrorCode code) noexcept {
  switch (code) {
    case ErrorCode::none:
      return "none";
    case ErrorCode::connection_closed:
      return "connection_closed";
    case ErrorCode::connection_not_reusable:
      return "connection_not_reusable";
    case ErrorCode::invalid_argument:
      return "invalid_argument";
    case ErrorCode::pipeline_depth_exceeded:
      return "pipeline_depth_exceeded";
    case ErrorCode::request_timeout:
      return "request_timeout";
    case ErrorCode::response_timeout:
      return "response_timeout";
    case ErrorCode::backend_error_limit:
      return "backend_error_limit";
    case ErrorCode::early_close:
      return "early_close";
    case ErrorCode::unexpected_response:
      return "unexpected_response";
    case ErrorCode::out_of_order_response:
      return "out_of_order_response";
    case ErrorCode::malformed_response:
      return "malformed_response";
    case ErrorCode::header_too_large:
      return "header_too_large";
    case ErrorCode::body_too_large:
      return "body_too_large";
    case ErrorCode::framing_ambiguous:
      return "framing_ambiguous";
    case ErrorCode::unsupported_framing:
      return "unsupported_framing";
    case ErrorCode::method_mismatch:
      return "method_mismatch";
    case ErrorCode::status_mismatch:
      return "status_mismatch";
    case ErrorCode::length_mismatch:
      return "length_mismatch";
  }
  return "unknown";
}

const std::string* Response::find_header(std::string_view name) const noexcept {
  for (const Header& header : headers) {
    if (iequals(header.name, name)) {
      return &header.value;
    }
  }
  return nullptr;
}

ConnectionPairer::ConnectionPairer() : ConnectionPairer(Config{}) {}

ConnectionPairer::ConnectionPairer(Config config)
    : config_(std::move(config)) {
  if (config_.max_pipeline_depth == 0) {
    config_.max_pipeline_depth = 1;
  }
  if (config_.max_header_bytes == 0) {
    config_.max_header_bytes = 1;
  }
  if (config_.max_header_count == 0) {
    config_.max_header_count = 1;
  }
  if (config_.max_frame_overhead_bytes == 0) {
    config_.max_frame_overhead_bytes = 1;
  }
  if (config_.max_backend_errors == 0) {
    config_.max_backend_errors = 1;
  }
  if (config_.request_timeout.count() < 0) {
    config_.request_timeout = std::chrono::milliseconds{0};
  }
  if (config_.response_timeout.count() < 0) {
    config_.response_timeout = std::chrono::milliseconds{0};
  }
}

bool ConnectionPairer::queue_id_exists(std::uint64_t id) const noexcept {
  for (const PendingRequest& pending : queue_) {
    if (pending.request.id == id) {
      return true;
    }
  }
  return false;
}

std::uint64_t ConnectionPairer::allocate_id() noexcept {
  while (queue_id_exists(next_auto_id_)) {
    ++next_auto_id_;
    if (next_auto_id_ == 0) {
      next_auto_id_ = 1;
    }
  }
  return next_auto_id_++;
}

bool ConnectionPairer::idle() const noexcept {
  return !closed_ && queue_.empty() && !current_in_progress_ && available() == 0;
}

bool ConnectionPairer::reusable() const noexcept {
  return idle() && !backend_will_close_ && backend_errors_ == 0 &&
         (!config_.require_explicit_reuse || !needs_reuse_);
}

Result ConnectionPairer::enqueue(Request request, TimePoint now) {
  if (closed_) {
    return fail(ErrorCode::connection_closed, "connection is closed", false);
  }
  if (needs_reuse_ && config_.require_explicit_reuse) {
    return fail(ErrorCode::connection_not_reusable,
                "prepare_reuse() is required before another request", false);
  }
  if (backend_will_close_) {
    return fail(ErrorCode::connection_not_reusable,
                "backend indicated that the connection will close", false);
  }
  if (!valid_token(request.method)) {
    return fail(ErrorCode::invalid_argument, "invalid request method", false);
  }
  if (!parse_http_version(request.version)) {
    return fail(ErrorCode::invalid_argument, "unsupported request version",
                false);
  }
  if (request.expected_status.has_value() &&
      (*request.expected_status < 100 || *request.expected_status > 599)) {
    return fail(ErrorCode::invalid_argument, "expected status out of range",
                false);
  }
  if (request.id == 0) {
    request.id = allocate_id();
  } else if (queue_id_exists(request.id)) {
    return fail(ErrorCode::invalid_argument,
                "duplicate request id within the connection", false);
  }
  if (queue_.size() >= config_.max_pipeline_depth) {
    return fail(ErrorCode::pipeline_depth_exceeded,
                "pipeline depth limit reached", false);
  }

  queue_.push_back(PendingRequest{std::move(request), now});
  needs_reuse_ = false;
  last_failure_ = Failure{};
  return {};
}

Result ConnectionPairer::fail(ErrorCode code, std::string detail, bool fatal,
                              Result partial) {
  Failure failure;
  failure.code = code;
  failure.fatal = fatal;
  failure.detail = std::move(detail);
  last_failure_ = failure;
  partial.failure = failure;
  if (fatal) {
    poison(code, failure.detail);
  }
  return partial;
}

void ConnectionPairer::poison(ErrorCode /*code*/, std::string /*detail*/) noexcept {
  closed_ = true;
  clear_all_state();
}

void ConnectionPairer::clear_all_state() noexcept {
  queue_.clear();
  buffer_.clear();
  buffer_offset_ = 0;
  reset_parser();
  backend_will_close_ = false;
  needs_reuse_ = false;
  current_in_progress_ = false;
  interim_responses_ = 0;
  backend_errors_ = 0;
  ++generation_;
}

void ConnectionPairer::reset_parser() noexcept {
  parse_state_ = ParseState::head;
  current_ = Response{};
  current_framing_ = Framing{};
  remaining_ = 0;
  current_body_bytes_ = 0;
  trailer_bytes_ = 0;
  frame_overhead_bytes_ = 0;
  response_head_seen_ = false;
  response_started_at_ = TimePoint{};
}

void ConnectionPairer::consume(std::size_t bytes) noexcept {
  const std::size_t amount = std::min(bytes, available());
  buffer_offset_ += amount;
  if (buffer_offset_ > 64 * 1024 &&
      buffer_offset_ * 2 >= buffer_.size()) {
    buffer_.erase(0, buffer_offset_);
    buffer_offset_ = 0;
  }
  if (buffer_offset_ == buffer_.size()) {
    buffer_.clear();
    buffer_offset_ = 0;
  }
}

void ConnectionPairer::close() noexcept {
  if (!closed_) {
    closed_ = true;
    clear_all_state();
  }
}

Result ConnectionPairer::poll(TimePoint now) {
  if (closed_) {
    return fail(ErrorCode::connection_closed, "connection is closed", false);
  }
  if (queue_.empty() && !current_in_progress_) {
    return {};
  }
  if (current_in_progress_) {
    if (config_.response_timeout.count() >= 0 &&
        now - response_started_at_ >= config_.response_timeout) {
      return fail(ErrorCode::response_timeout,
                  "response body did not complete before its deadline");
    }
    return {};
  }
  if (now - queue_.front().queued_at >= config_.request_timeout) {
    return fail(ErrorCode::request_timeout,
                "request did not receive a response before its deadline");
  }
  return {};
}

Result ConnectionPairer::on_backend_error(std::string detail, TimePoint now) {
  if (closed_) {
    return fail(ErrorCode::connection_closed, "connection is closed", false);
  }
  Result timeout = poll(now);
  if (timeout.failure.fatal) {
    return timeout;
  }
  ++backend_errors_;
  const bool active = !queue_.empty() || current_in_progress_;
  if (active || backend_errors_ >= config_.max_backend_errors) {
    if (detail.empty()) {
      detail = "backend error limit reached";
    }
    return fail(ErrorCode::backend_error_limit, std::move(detail));
  }
  Failure warning;
  warning.code = ErrorCode::backend_error_limit;
  warning.fatal = false;
  warning.detail = std::move(detail);
  last_failure_ = warning;
  Result result;
  result.failure = warning;
  return result;
}

Result ConnectionPairer::prepare_reuse(TimePoint now) {
  if (closed_) {
    return fail(ErrorCode::connection_closed, "connection is closed", false);
  }
  Result timeout = poll(now);
  if (timeout.failure.fatal) {
    return timeout;
  }
  if (!queue_.empty() || current_in_progress_ || available() != 0 ||
      backend_will_close_ || response_head_seen_) {
    return fail(ErrorCode::connection_not_reusable,
                "connection state is not clear for reuse");
  }

  reset_parser();
  buffer_.clear();
  buffer_offset_ = 0;
  interim_responses_ = 0;
  backend_errors_ = 0;
  needs_reuse_ = false;
  backend_will_close_ = false;
  last_failure_ = Failure{};
  ++generation_;
  return {};
}

bool ConnectionPairer::analyze_response_head(
    Response response, const PendingRequest& pending, HeadAnalysis& analysis,
    Failure& failure, bool raw_stream) {
  if (!parse_http_version(response.version)) {
    failure.code = ErrorCode::malformed_response;
    failure.detail = "unsupported response HTTP version";
    return false;
  }
  if (response.status < 100 || response.status > 599) {
    failure.code = ErrorCode::malformed_response;
    failure.detail = "response status out of range";
    return false;
  }
  if (!valid_reason(response.reason)) {
    failure.code = ErrorCode::malformed_response;
    failure.detail = "invalid response reason phrase";
    return false;
  }
  if (response.headers.size() > config_.max_header_count) {
    failure.code = ErrorCode::header_too_large;
    failure.detail = "too many response header fields";
    return false;
  }
  for (const Header& header : response.headers) {
    if (!valid_token(header.name) || !valid_header_value(header.value)) {
      failure.code = ErrorCode::malformed_response;
      failure.detail = "invalid response header field";
      return false;
    }
  }

  ParsedHead head;
  head.status = response.status;
  head.version = response.version;
  head.reason = response.reason;
  head.headers = response.headers;

  FramingError framing_error = FramingError::none;
  std::string framing_detail;
  HeaderFraming framing =
      inspect_framing_headers(head, response, framing_error, framing_detail);
  if (framing_error != FramingError::none) {
    failure.code =
        framing_error == FramingError::malformed
            ? ErrorCode::malformed_response
            : (framing_error == FramingError::ambiguous
                   ? ErrorCode::framing_ambiguous
                   : ErrorCode::unsupported_framing);
    failure.detail = std::move(framing_detail);
    return false;
  }

  if (response.status == 101) {
    failure.code = ErrorCode::unsupported_framing;
    failure.detail =
        "protocol switching cannot be paired as an HTTP response stream";
    return false;
  }

  const bool head_method = pending.request.method == "HEAD";
  const bool connect_method = pending.request.method == "CONNECT";
  const bool bodyless_status =
      is_interim_status(response.status) || response.status == 204 ||
      response.status == 304;

  if (is_interim_status(response.status)) {
    if (framing.has_content_length || framing.chunked) {
      failure.code = ErrorCode::malformed_response;
      failure.detail = "interim response must not carry body framing";
      return false;
    }
    if (!response.body.empty()) {
      failure.code = ErrorCode::length_mismatch;
      failure.detail = "interim response must not carry a body";
      return false;
    }
    if (response.status >= 200) {
      failure.code = ErrorCode::malformed_response;
      failure.detail = "invalid interim status";
      return false;
    }
  }

  if (connect_method && response.status >= 200 && response.status <= 299) {
    failure.code = ErrorCode::unsupported_framing;
    failure.detail =
        "CONNECT tunnel bytes cannot be paired by the HTTP response queue";
    return false;
  }

  if (response.status == 204 &&
      (framing.chunked ||
       (framing.has_content_length && framing.content_length != 0))) {
    failure.code = ErrorCode::length_mismatch;
    failure.detail = "204 response must not declare a message body";
    return false;
  }

  Framing result;
  result.bodyless = head_method || bodyless_status;
  result.chunked = framing.chunked;
  result.has_content_length = framing.has_content_length;
  result.content_length = framing.content_length;
  result.connection_close = framing.connection_close;

  if (!result.bodyless) {
    if (result.chunked) {
      // Framed by chunks below.
    } else if (result.has_content_length) {
      if (result.content_length > config_.max_body_bytes) {
        failure.code = ErrorCode::body_too_large;
        failure.detail = "declared response body exceeds the limit";
        return false;
      }
    } else {
      if (result.connection_close || response.close_delimited) {
        if (!config_.allow_close_delimited) {
          failure.code = ErrorCode::unsupported_framing;
          failure.detail = "response has no length framing";
          return false;
        }
        if (!queue_.empty() && queue_.size() > 1) {
          failure.code = ErrorCode::framing_ambiguous;
          failure.detail =
              "close-delimited response cannot be paired with later requests";
          return false;
        }
        result.unknown_length = true;
        result.connection_close = true;
      } else if (!raw_stream) {
        // The structured entry point receives an already completed response
        // object. Its body boundary is therefore authoritative; still enforce
        // the configured bound and any explicitly declared length.
        if (response.body.size() > config_.max_body_bytes) {
          failure.code = ErrorCode::body_too_large;
          failure.detail = "response body exceeds the limit";
          return false;
        }
        result.has_content_length = true;
        result.content_length = response.body.size();
      } else {
        failure.code = ErrorCode::framing_ambiguous;
        failure.detail =
            "response without Content-Length or chunked framing cannot be "
            "confirmed on a reusable connection";
        return false;
      }
    }
  }

  analysis.framing = result;
  analysis.response = std::move(response);
  analysis.response.content_length =
      result.has_content_length
          ? std::optional<std::uint64_t>(result.content_length)
          : std::nullopt;
  analysis.response.chunked = result.chunked;
  analysis.response.connection_close = result.connection_close;
  analysis.response.close_delimited = result.unknown_length;

  if (is_interim_status(analysis.response.status)) {
    return true;
  }

  if (pending.request.expected_status.has_value() &&
      *pending.request.expected_status != analysis.response.status) {
    failure.code = ErrorCode::status_mismatch;
    failure.detail = "response status does not match the request contract";
    return false;
  }

  if (!config_.response_id_header.empty()) {
    const std::string expected = expected_binding(pending.request);
    std::size_t matches = 0;
    bool value_matches = false;
    for (const Header& header : analysis.response.headers) {
      if (!iequals(header.name, config_.response_id_header)) {
        continue;
      }
      ++matches;
      value_matches = trim_ows(header.value) == expected;
    }
    if (matches != 1 || !value_matches) {
      failure.code = ErrorCode::out_of_order_response;
      failure.detail =
          "response binding header is missing, duplicated, or belongs to "
          "another request";
      return false;
    }
  }

  (void)raw_stream;
  return true;
}

bool ConnectionPairer::validate_body_and_pair(
    const Request& request, const HeadAnalysis& analysis,
    const Response& response, Failure& failure,
    std::size_t outstanding) const {
  if (is_interim_status(response.status)) {
    failure.code = ErrorCode::unexpected_response;
    failure.detail = "interim response cannot complete a request";
    return false;
  }
  if (request.expected_status.has_value() &&
      *request.expected_status != response.status) {
    failure.code = ErrorCode::status_mismatch;
    failure.detail = "response status does not match the request contract";
    return false;
  }
  if (response.status == 101) {
    failure.code = ErrorCode::unsupported_framing;
    failure.detail = "protocol switching is not supported";
    return false;
  }

  const bool head_method = request.method == "HEAD";
  if (analysis.framing.bodyless) {
    if (!response.body.empty()) {
      failure.code =
          head_method ? ErrorCode::method_mismatch
                      : ErrorCode::length_mismatch;
      failure.detail = "response body was supplied for a bodyless response";
      return false;
    }
    if (outstanding > 1 && config_.response_id_header.empty() &&
        (analysis.framing.chunked ||
         (analysis.framing.has_content_length &&
          analysis.framing.content_length > 0))) {
      failure.code = ErrorCode::framing_ambiguous;
      failure.detail =
          head_method
              ? "HEAD response with representation length cannot be "
                "distinguished from a reordered body-bearing response"
              : "bodyless response with representation length cannot be "
                "distinguished from a reordered body-bearing response";
      return false;
    }
    return true;
  }

  if (response.body.size() > config_.max_body_bytes) {
    failure.code = ErrorCode::body_too_large;
    failure.detail = "response body exceeds the limit";
    return false;
  }
  if (analysis.framing.chunked) {
    return true;
  }
  if (analysis.framing.has_content_length) {
    if (response.body.size() != analysis.framing.content_length) {
      failure.code = ErrorCode::length_mismatch;
      failure.detail = "response body length differs from Content-Length";
      return false;
    }
    return true;
  }
  if (analysis.framing.unknown_length) {
    if (!analysis.framing.connection_close &&
        !response.close_delimited) {
      failure.code = ErrorCode::framing_ambiguous;
      failure.detail = "response body length cannot be confirmed";
      return false;
    }
    return true;
  }

  failure.code = ErrorCode::framing_ambiguous;
  failure.detail = "response body framing cannot be confirmed";
  return false;
}

Result ConnectionPairer::on_response(Response response, TimePoint now) {
  if (response.request_id.has_value()) {
    return on_response(*response.request_id, std::move(response), now);
  }
  if (closed_) {
    return fail(ErrorCode::connection_closed, "connection is closed", false);
  }
  Result timeout = poll(now);
  if (timeout.failure.fatal) {
    return timeout;
  }
  if (queue_.empty()) {
    return fail(ErrorCode::unexpected_response,
                "response arrived with no queued request");
  }
  if (current_in_progress_) {
    return fail(ErrorCode::unexpected_response,
                "a second structured response arrived before the first "
                "completed");
  }

  HeadAnalysis analysis;
  Failure failure;
  if (!analyze_response_head(std::move(response), queue_.front(), analysis,
                             failure, false)) {
    return fail(failure.code, failure.detail);
  }

  if (is_interim_status(analysis.response.status)) {
    ++interim_responses_;
    if (interim_responses_ > config_.max_interim_responses) {
      return fail(ErrorCode::unexpected_response,
                  "too many interim responses");
    }
    last_failure_ = Failure{};
    return {};
  }

  if (!validate_body_and_pair(queue_.front().request, analysis,
                              analysis.response, failure, queue_.size())) {
    return fail(failure.code, failure.detail);
  }

  const bool will_close =
      analysis.framing.connection_close || analysis.response.close_delimited ||
      analysis.framing.unknown_length;
  Result result;
  Delivery delivery;
  delivery.request = queue_.front().request;
  delivery.response = std::move(analysis.response);
  delivery.response.request_id = delivery.request.id;
  result.deliveries.push_back(std::move(delivery));
  queue_.pop_front();
  interim_responses_ = 0;

  if (will_close) {
    backend_will_close_ = true;
    if (!queue_.empty()) {
      return fail(ErrorCode::early_close,
                  "backend closed while pipelined requests were outstanding",
                  true, std::move(result));
    }
    closed_ = true;
    clear_all_state();
    last_failure_ = Failure{};
    return result;
  }
  if (queue_.empty()) {
    needs_reuse_ = true;
  }
  last_failure_ = Failure{};
  return result;
}

Result ConnectionPairer::on_response(std::uint64_t request_id,
                                     Response response, TimePoint now) {
  if (closed_) {
    return fail(ErrorCode::connection_closed, "connection is closed", false);
  }
  Result timeout = poll(now);
  if (timeout.failure.fatal) {
    return timeout;
  }
  if (queue_.empty()) {
    return fail(ErrorCode::unexpected_response,
                "response arrived with no queued request");
  }
  if (request_id != queue_.front().request.id) {
    return fail(ErrorCode::out_of_order_response,
                "response id does not match the head of the request queue");
  }
  response.request_id.reset();
  return on_response(std::move(response), now);
}

Result ConnectionPairer::feed(std::string_view bytes, TimePoint now) {
  if (closed_) {
    return fail(ErrorCode::connection_closed, "connection is closed", false);
  }
  Result result = poll(now);
  if (result.failure.fatal) {
    return result;
  }
  if (bytes.empty()) {
    return result;
  }

  const std::size_t max_buffer =
      config_.max_header_bytes + config_.max_body_bytes +
      config_.max_frame_overhead_bytes;
  std::size_t offset = 0;
  while (offset < bytes.size() && !closed_) {
    const std::size_t free_space =
        max_buffer > available() ? max_buffer - available() : 0;
    if (free_space == 0) {
      return fail(ErrorCode::body_too_large,
                  "response buffer limit reached", true,
                  std::move(result));
    }
    const std::size_t take =
        std::min(free_space, bytes.size() - offset);
    buffer_.append(bytes.data() + offset, take);
    offset += take;
    result = process_input(now, std::move(result));
  }
  if (offset < bytes.size() && !closed_) {
    return fail(ErrorCode::body_too_large,
                "response buffer limit reached", true, std::move(result));
  }
  if (result.failure.code == ErrorCode::none) {
    last_failure_ = Failure{};
  }
  return result;
}

Result ConnectionPairer::process_input(TimePoint now, Result partial) {
  while (!closed_) {
    if (queue_.empty()) {
      if (available() != 0) {
        return fail(ErrorCode::unexpected_response,
                    "response bytes arrived with no queued request", true,
                    std::move(partial));
      }
      return partial;
    }

    if (current_in_progress_) {
      if (now - response_started_at_ >= config_.response_timeout) {
        return fail(ErrorCode::response_timeout,
                    "response body did not complete before its deadline",
                    true, std::move(partial));
      }
    }

    const std::size_t available_before = available();
    const ParseState state_before = parse_state_;
    const std::size_t queue_before = queue_.size();
    const bool progress_before = current_in_progress_;
    switch (parse_state_) {
      case ParseState::head:
        partial = process_head(now, std::move(partial));
        break;
      case ParseState::identity_body:
        partial = process_identity(now, std::move(partial));
        break;
      case ParseState::chunk_size:
        partial = process_chunk_size(now, std::move(partial));
        break;
      case ParseState::chunk_data:
        partial = process_chunk_data(now, std::move(partial));
        break;
      case ParseState::chunk_data_crlf:
        partial = process_chunk_crlf(now, std::move(partial));
        break;
      case ParseState::trailers:
        partial = process_trailers(now, std::move(partial));
        break;
      case ParseState::close_body:
        partial = process_close_body(std::move(partial));
        break;
    }
    if (closed_) {
      return partial;
    }
    if (available() == available_before && parse_state_ == state_before &&
        queue_.size() == queue_before &&
        current_in_progress_ == progress_before) {
      return partial;
    }
  }
  return partial;
}

Result ConnectionPairer::process_head(TimePoint now, Result partial) {
  const std::string_view view = buffer_.data() + buffer_offset_;
  const std::size_t end = view.find(kHeadEnd);
  const std::size_t bytes_available = available();
  if (end == std::string_view::npos) {
    if (bytes_available > config_.max_header_bytes) {
      return fail(ErrorCode::header_too_large,
                  "response header exceeds the limit", true,
                  std::move(partial));
    }
    return partial;
  }
  if (end + kHeadEnd.size() > config_.max_header_bytes) {
    return fail(ErrorCode::header_too_large,
                "response header exceeds the limit", true,
                std::move(partial));
  }
  if (frame_overhead_bytes_ + end + kHeadEnd.size() >
      config_.max_frame_overhead_bytes) {
    return fail(ErrorCode::header_too_large,
                "response framing overhead exceeds the limit", true,
                std::move(partial));
  }

  ParsedHead head;
  std::string parse_error;
  if (!parse_head_block(view.substr(0, end), head, config_.max_header_count,
                        parse_error)) {
    return fail(ErrorCode::malformed_response,
                "invalid response head: " + parse_error, true,
                std::move(partial));
  }
  consume(end + kHeadEnd.size());
  frame_overhead_bytes_ += end + kHeadEnd.size();

  if (head.status == 101) {
    return fail(ErrorCode::unsupported_framing,
                "protocol switching cannot be paired as an HTTP stream", true,
                std::move(partial));
  }

  if (is_interim_status(head.status)) {
    for (const Header& header : head.headers) {
      if (iequals(header.name, "content-length") ||
          iequals(header.name, "transfer-encoding")) {
        return fail(ErrorCode::malformed_response,
                    "interim response must not carry body framing", true,
                    std::move(partial));
      }
    }
    ++interim_responses_;
    if (interim_responses_ > config_.max_interim_responses) {
      return fail(ErrorCode::unexpected_response,
                  "too many interim responses", true, std::move(partial));
    }
    return partial;
  }

  if (queue_.empty()) {
    return fail(ErrorCode::unexpected_response,
                "response arrived with no queued request", true,
                std::move(partial));
  }

  Response response;
  response.status = head.status;
  response.reason = std::move(head.reason);
  response.version = std::move(head.version);
  response.headers = std::move(head.headers);
  response.request_id = queue_.front().request.id;

  HeadAnalysis analysis;
  Failure failure;
  if (!analyze_response_head(std::move(response), queue_.front(), analysis,
                             failure, true)) {
    return fail(failure.code, failure.detail, true, std::move(partial));
  }

  current_ = std::move(analysis.response);
  current_framing_ = analysis.framing;
  current_in_progress_ = true;
  response_head_seen_ = true;
  response_started_at_ = now;
  current_body_bytes_ = 0;
  remaining_ = 0;
  interim_responses_ = 0;

  if (current_framing_.bodyless) {
    return complete_current(now, std::move(partial));
  }
  if (current_framing_.chunked) {
    parse_state_ = ParseState::chunk_size;
    return partial;
  }
  if (current_framing_.has_content_length) {
    remaining_ = current_framing_.content_length;
    if (remaining_ == 0) {
      return complete_current(now, std::move(partial));
    }
    parse_state_ = ParseState::identity_body;
    return partial;
  }
  if (current_framing_.unknown_length) {
    current_.close_delimited = true;
    parse_state_ = ParseState::close_body;
    return partial;
  }

  return fail(ErrorCode::framing_ambiguous,
              "response body framing cannot be confirmed", true,
              std::move(partial));
}

Result ConnectionPairer::process_identity(TimePoint now, Result partial) {
  if (available() == 0) {
    return partial;
  }
  const std::size_t take =
      static_cast<std::size_t>(std::min<std::uint64_t>(available(), remaining_));
  current_.body.append(buffer_.data() + buffer_offset_, take);
  consume(take);
  remaining_ -= take;
  current_body_bytes_ += take;
  if (current_body_bytes_ > config_.max_body_bytes) {
    return fail(ErrorCode::body_too_large, "response body exceeds the limit",
                true, std::move(partial));
  }
  if (remaining_ == 0) {
    return complete_current(now, std::move(partial));
  }
  return partial;
}

Result ConnectionPairer::process_chunk_size(TimePoint now, Result partial) {
  (void)now;
  const std::string_view view = buffer_.data() + buffer_offset_;
  const std::size_t end = view.find(kCrlf);
  if (end == std::string_view::npos) {
    if (available() > config_.max_header_bytes) {
      return fail(ErrorCode::header_too_large,
                  "chunk-size line exceeds the limit", true,
                  std::move(partial));
    }
    return partial;
  }
  if (end > config_.max_header_bytes) {
    return fail(ErrorCode::header_too_large,
                "chunk-size line exceeds the limit", true,
                std::move(partial));
  }
  if (frame_overhead_bytes_ + end + kCrlf.size() >
      config_.max_frame_overhead_bytes) {
    return fail(ErrorCode::header_too_large,
                "chunk framing overhead exceeds the limit", true,
                std::move(partial));
  }

  const std::string_view line = view.substr(0, end);
  const std::size_t semicolon = line.find(';');
  const std::string_view size_text =
      semicolon == std::string_view::npos ? line : line.substr(0, semicolon);
  std::uint64_t chunk_size = 0;
  if (!parse_hex(size_text, chunk_size)) {
    return fail(ErrorCode::malformed_response, "invalid chunk size", true,
                std::move(partial));
  }
  if (semicolon != std::string_view::npos) {
    const std::string_view extension = line.substr(semicolon + 1);
    for (const unsigned char c : extension) {
      if (c == '\r' || c == '\n' || c == '\0') {
        return fail(ErrorCode::malformed_response,
                    "invalid chunk extension", true, std::move(partial));
      }
    }
  }
  consume(end + kCrlf.size());
  frame_overhead_bytes_ += end + kCrlf.size();

  if (chunk_size == 0) {
    parse_state_ = ParseState::trailers;
    trailer_bytes_ = 0;
    return partial;
  }
  if (chunk_size > config_.max_body_bytes - current_body_bytes_) {
    return fail(ErrorCode::body_too_large, "chunked body exceeds the limit",
                true, std::move(partial));
  }
  remaining_ = chunk_size;
  parse_state_ = ParseState::chunk_data;
  return partial;
}

Result ConnectionPairer::process_chunk_data(TimePoint now, Result partial) {
  (void)now;
  if (available() == 0) {
    return partial;
  }
  const std::size_t take =
      static_cast<std::size_t>(std::min<std::uint64_t>(available(), remaining_));
  current_.body.append(buffer_.data() + buffer_offset_, take);
  consume(take);
  remaining_ -= take;
  current_body_bytes_ += take;
  if (current_body_bytes_ > config_.max_body_bytes) {
    return fail(ErrorCode::body_too_large, "response body exceeds the limit",
                true, std::move(partial));
  }
  if (remaining_ == 0) {
    parse_state_ = ParseState::chunk_data_crlf;
  }
  return partial;
}

Result ConnectionPairer::process_chunk_crlf(TimePoint now, Result partial) {
  (void)now;
  if (available() < kCrlf.size()) {
    return partial;
  }
  const std::string_view view = buffer_.data() + buffer_offset_;
  if (view.substr(0, kCrlf.size()) != kCrlf) {
    return fail(ErrorCode::malformed_response,
                "chunk data is not followed by CRLF", true,
                std::move(partial));
  }
  consume(kCrlf.size());
  frame_overhead_bytes_ += kCrlf.size();
  parse_state_ = ParseState::chunk_size;
  return partial;
}

Result ConnectionPairer::process_trailers(TimePoint now, Result partial) {
  const std::string_view view = buffer_.data() + buffer_offset_;
  const std::size_t end = view.find(kCrlf);
  if (end == std::string_view::npos) {
    if (available() > config_.max_header_bytes) {
      return fail(ErrorCode::header_too_large,
                  "chunk trailer exceeds the limit", true,
                  std::move(partial));
    }
    return partial;
  }
  if (end > config_.max_header_bytes) {
    return fail(ErrorCode::header_too_large,
                "chunk trailer exceeds the limit", true,
                std::move(partial));
  }
  if (frame_overhead_bytes_ + end + kCrlf.size() >
      config_.max_frame_overhead_bytes) {
    return fail(ErrorCode::header_too_large,
                "chunk trailer overhead exceeds the limit", true,
                std::move(partial));
  }

  const std::string_view line = view.substr(0, end);
  consume(end + kCrlf.size());
  frame_overhead_bytes_ += end + kCrlf.size();
  if (line.empty()) {
    return complete_current(now, std::move(partial));
  }

  Header trailer;
  std::string parse_error;
  if (!parse_header_line(line, trailer, parse_error)) {
    return fail(ErrorCode::malformed_response,
                "invalid chunk trailer: " + parse_error, true,
                std::move(partial));
  }
  if (iequals(trailer.name, "content-length") ||
      iequals(trailer.name, "transfer-encoding")) {
    return fail(ErrorCode::malformed_response,
                "forbidden framing field in chunk trailer", true,
                std::move(partial));
  }
  trailer_bytes_ += end + kCrlf.size();
  if (trailer_bytes_ > config_.max_header_bytes) {
    return fail(ErrorCode::header_too_large,
                "chunk trailers exceed the limit", true,
                std::move(partial));
  }
  return partial;
}

Result ConnectionPairer::process_close_body(Result partial) {
  if (available() == 0) {
    return partial;
  }
  current_.body.append(buffer_.data() + buffer_offset_, available());
  current_body_bytes_ += available();
  consume(available());
  current_.close_delimited = true;
  if (current_body_bytes_ > config_.max_body_bytes) {
    return fail(ErrorCode::body_too_large, "response body exceeds the limit",
                true, std::move(partial));
  }
  return partial;
}

Result ConnectionPairer::complete_current(TimePoint now, Result partial,
                                          bool end_of_stream) {
  if (!current_in_progress_ || queue_.empty()) {
    return fail(ErrorCode::unexpected_response,
                "internal response state does not match the request queue",
                true, std::move(partial));
  }

  HeadAnalysis analysis;
  analysis.framing = current_framing_;
  analysis.response = current_;
  Failure failure;
  if (!validate_body_and_pair(queue_.front().request, analysis,
                              analysis.response, failure, queue_.size())) {
    return fail(failure.code, failure.detail, true, std::move(partial));
  }

  const bool will_close =
      current_framing_.connection_close || current_.close_delimited ||
      current_framing_.unknown_length;
  Delivery delivery;
  delivery.request = queue_.front().request;
  delivery.response = std::move(current_);
  delivery.response.request_id = delivery.request.id;
  partial.deliveries.push_back(std::move(delivery));
  queue_.pop_front();
  interim_responses_ = 0;

  current_in_progress_ = false;
  reset_parser();

  if (will_close) {
    backend_will_close_ = true;
    if (!queue_.empty()) {
      return fail(ErrorCode::early_close,
                  "backend closed while pipelined requests were outstanding",
                  true, std::move(partial));
    }
    closed_ = true;
    clear_all_state();
    last_failure_ = Failure{};
    return partial;
  }
  if (queue_.empty()) {
    needs_reuse_ = true;
  }
  (void)end_of_stream;
  (void)now;
  return partial;
}

Result ConnectionPairer::end_of_stream(TimePoint now) {
  if (closed_) {
    return fail(ErrorCode::connection_closed, "connection is closed", false);
  }
  Result result = poll(now);
  if (result.failure.fatal) {
    return result;
  }

  if (queue_.empty() && available() == 0 && !current_in_progress_) {
    closed_ = true;
    clear_all_state();
    last_failure_ = Failure{};
    return result;
  }

  if (parse_state_ == ParseState::close_body && current_in_progress_) {
    if (available() != 0) {
      current_.body.append(buffer_.data() + buffer_offset_, available());
      current_body_bytes_ += available();
      consume(available());
      current_.close_delimited = true;
      if (current_body_bytes_ > config_.max_body_bytes) {
        return fail(ErrorCode::body_too_large,
                    "response body exceeds the limit", true,
                    std::move(result));
      }
    }
    return complete_current(now, std::move(result), true);
  }

  if (queue_.empty() && available() != 0) {
    return fail(ErrorCode::unexpected_response,
                "unexpected response bytes at backend EOF", true,
                std::move(result));
  }
  return fail(ErrorCode::early_close,
              "backend closed before every pipelined response completed", true,
              std::move(result));
}

}  // namespace netsec
