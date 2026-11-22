#include <charconv>
#include <fstream>
#include <iostream>
#include <optional>
#include <sstream>
#include <string>
#include <string_view>
#include <vector>

#include "gsb/evidence.hpp"
#include "gsb/json.hpp"
#include "gsb/model.hpp"
#include "gsb/solver.hpp"

namespace {

bool readFile(const std::string& path, std::string* out, std::string* error) {
  std::ifstream stream(path, std::ios::binary);
  if (!stream) {
    *error = "cannot open '" + path + "'";
    return false;
  }
  std::ostringstream buffer;
  buffer << stream.rdbuf();
  *out = buffer.str();
  return true;
}

bool writeFile(const std::string& path, std::string_view data, std::string* error) {
  std::ofstream stream(path, std::ios::binary);
  if (!stream) {
    *error = "cannot create '" + path + "'";
    return false;
  }
  stream.write(data.data(), static_cast<std::streamsize>(data.size()));
  if (!stream) {
    *error = "failed writing '" + path + "'";
    return false;
  }
  return true;
}

bool parseU64(std::string_view text, uint64_t* out) {
  uint64_t value = 0;
  auto result = std::from_chars(text.data(), text.data() + text.size(), value);
  if (result.ec != std::errc() || result.ptr != text.data() + text.size()) {
    return false;
  }
  *out = value;
  return true;
}

void usage() {
  std::cout
      << "gsb-plan - importable redaction planning package\n\n"
      << "Usage:\n"
      << "  gsb-plan solve --input INPUT.json [--out PLAN.json] [--node-budget N]\n"
      << "  gsb-plan validate --input INPUT.json --plan PLAN.json [--no-optimality]\n"
      << "  gsb-plan inspect --input INPUT.json --plan PLAN.json --export ID\n"
      << "                    --start N --length N [--max-inspect N] [--out FILE]\n\n"
      << "The input and plan are JSON (gsb.redaction.input/v1 and\n"
      << "gsb.redaction.plan/v1).  Evidence contains mask bits and provenance,\n"
      << "never page byte values.\n";
}

struct Args {
  std::string command;
  std::string input;
  std::string plan;
  std::string out;
  std::string export_id;
  std::optional<uint64_t> start;
  std::optional<uint64_t> length;
  std::optional<uint64_t> node_budget;
  std::optional<uint64_t> max_inspect;
  bool check_optimality = true;
};

bool parseArgs(int argc, char** argv, Args* args, std::string* error) {
  if (argc < 2) {
    *error = "missing command";
    return false;
  }
  args->command = argv[1];
  for (int i = 2; i < argc; ++i) {
    std::string arg = argv[i];
    auto need_value = [&](std::string* value) -> bool {
      if (i + 1 >= argc) {
        *error = "missing value after " + arg;
        return false;
      }
      *value = argv[++i];
      return true;
    };
    if (arg == "--input") {
      if (!need_value(&args->input)) return false;
    } else if (arg == "--plan") {
      if (!need_value(&args->plan)) return false;
    } else if (arg == "--out") {
      if (!need_value(&args->out)) return false;
    } else if (arg == "--export") {
      if (!need_value(&args->export_id)) return false;
    } else if (arg == "--start") {
      std::string value;
      if (!need_value(&value)) return false;
      uint64_t parsed = 0;
      if (!parseU64(value, &parsed)) {
        *error = "invalid --start";
        return false;
      }
      args->start = parsed;
    } else if (arg == "--length") {
      std::string value;
      if (!need_value(&value)) return false;
      uint64_t parsed = 0;
      if (!parseU64(value, &parsed)) {
        *error = "invalid --length";
        return false;
      }
      args->length = parsed;
    } else if (arg == "--node-budget") {
      std::string value;
      if (!need_value(&value)) return false;
      uint64_t parsed = 0;
      if (!parseU64(value, &parsed)) {
        *error = "invalid --node-budget";
        return false;
      }
      args->node_budget = parsed;
    } else if (arg == "--max-inspect") {
      std::string value;
      if (!need_value(&value)) return false;
      uint64_t parsed = 0;
      if (!parseU64(value, &parsed)) {
        *error = "invalid --max-inspect";
        return false;
      }
      args->max_inspect = parsed;
    } else if (arg == "--no-optimality") {
      args->check_optimality = false;
    } else if (arg == "--help" || arg == "-h") {
      usage();
      std::exit(0);
    } else {
      *error = "unknown option '" + arg + "'";
      return false;
    }
  }
  return true;
}

bool emit(const std::string& out_path, const std::string& text, std::string* error) {
  if (out_path.empty()) {
    std::cout << text;
    return true;
  }
  return writeFile(out_path, text, error);
}

bool loadInput(const std::string& path, gsb::Model* model, std::string* error) {
  std::string text;
  if (!readFile(path, &text, error)) return false;
  gsb::LoadResult loaded = gsb::loadModel(std::move(text));
  if (!loaded.ok()) {
    *error = "input error:";
    for (const std::string& item : loaded.errors) *error += "\n  " + item;
    return false;
  }
  *model = std::move(*loaded.model);
  return true;
}

bool loadPlan(const std::string& path, gsb::Json* plan, std::string* error) {
  std::string text;
  if (!readFile(path, &text, error)) return false;
  std::string parse_error;
  *plan = gsb::Json::parse(text, &parse_error);
  if (!parse_error.empty()) {
    *error = "plan JSON parse error: " + parse_error;
    return false;
  }
  return true;
}

int runSolve(const Args& args) {
  std::string error;
  gsb::Model model;
  if (!loadInput(args.input, &model, &error)) {
    std::cerr << error << "\n";
    return 1;
  }
  gsb::SolveOptions options;
  options.node_budget =
      args.node_budget.value_or(model.config.node_budget);
  gsb::SolveResult solved = gsb::solve(model, options);
  gsb::Json plan = gsb::buildPlanJson(model, solved);
  if (!emit(args.out, plan.dump(2), &error)) {
    std::cerr << error << "\n";
    return 1;
  }
  return 0;
}

int runValidate(const Args& args) {
  std::string error;
  gsb::Model model;
  if (!loadInput(args.input, &model, &error)) {
    std::cerr << error << "\n";
    return 1;
  }
  gsb::Json plan;
  if (!loadPlan(args.plan, &plan, &error)) {
    std::cerr << error << "\n";
    return 1;
  }
  gsb::SolveOptions options;
  options.node_budget = args.node_budget.value_or(model.config.node_budget);
  gsb::ValidationResult validation =
      gsb::validatePlan(model, plan, args.check_optimality, options);

  gsb::Json::Object report;
  report.emplace("valid", gsb::Json(validation.valid));
  report.emplace("message", gsb::Json(validation.message));
  gsb::Json::Array errors;
  for (const std::string& item : validation.errors) errors.emplace_back(item);
  report.emplace("errors", gsb::Json(std::move(errors)));
  gsb::Json::Array warnings;
  for (const std::string& item : validation.warnings) warnings.emplace_back(item);
  report.emplace("warnings", gsb::Json(std::move(warnings)));
  report.emplace("optimality_checked",
                 gsb::Json(validation.optimality_checked));
  report.emplace("optimality_verified",
                 gsb::Json(validation.optimality_verified));
  report.emplace("recomputed_status",
                 gsb::Json(gsb::solveStatusName(validation.recomputed.status)));
  gsb::Json output(std::move(report));
  if (!emit(args.out, output.dump(2), &error)) {
    std::cerr << error << "\n";
    return 1;
  }
  return validation.valid ? 0 : 1;
}

int runInspect(const Args& args) {
  if (args.export_id.empty() || !args.start || !args.length) {
    std::cerr << "inspect requires --export, --start and --length\n";
    return 1;
  }
  std::string error;
  gsb::Model model;
  if (!loadInput(args.input, &model, &error)) {
    std::cerr << error << "\n";
    return 1;
  }
  gsb::Json plan;
  if (!loadPlan(args.plan, &plan, &error)) {
    std::cerr << error << "\n";
    return 1;
  }
  uint64_t max_length = args.max_inspect.value_or(model.config.max_inspect);
  gsb::EvidenceResult evidence = gsb::inspectRange(
      model, plan, args.export_id, *args.start, *args.length, max_length);
  if (!evidence.ok) {
    std::cerr << evidence.error << "\n";
    return 1;
  }
  if (!emit(args.out, evidence.evidence.dump(2), &error)) {
    std::cerr << error << "\n";
    return 1;
  }
  return 0;
}

}  // namespace

int main(int argc, char** argv) {
  if (argc == 1) {
    usage();
    return 1;
  }
  std::string first = argv[1];
  if (first == "--help" || first == "-h" || first == "help") {
    usage();
    return 0;
  }
  Args args;
  std::string error;
  if (!parseArgs(argc, argv, &args, &error)) {
    std::cerr << error << "\n";
    return 1;
  }
  if (args.input.empty()) {
    std::cerr << "--input is required\n";
    return 1;
  }
  if (args.command == "solve") return runSolve(args);
  if (args.command == "validate") return runValidate(args);
  if (args.command == "inspect") return runInspect(args);
  std::cerr << "unknown command '" << args.command << "'\n";
  return 1;
}
