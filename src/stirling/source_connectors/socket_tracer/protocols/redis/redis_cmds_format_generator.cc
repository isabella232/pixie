#include <absl/container/flat_hash_set.h>
#include <absl/strings/str_split.h>
#include <absl/strings/strip.h>

#include "src/common/base/base.h"

DEFINE_string(
    redis_cmdargs, "",
    "A text file. Produced with:\n"
    "curl https://redis.io/commands > redis_commands\n"
    R"(xmllint --html --xpath '//span[@class="command"]/text() | //span[@class="args"]/text()' )"
    R"(redis_commands | grep -o "\S.*\S")"
    "\n\nThe above command produces a text file. It contains all of Redis commands. "
    "Each command is followed by 0 or more lines of arguments description lines.");

DEFINE_string(redis_cmds, "", "A text file lists all Redis command names on each line.");

using ::pl::ReadFileToString;
using ::pl::Status;
using ::pl::VectorView;

// Returns a list of string lists. Each string list contains command name, 0 or more command
// arguments descriptions.
Status Main(std::string_view redis_cmds, std::string_view redis_cmdargs,
            std::vector<std::vector<std::string_view>>* commands) {
  absl::flat_hash_set<std::string_view> redis_cmd_set =
      absl::StrSplit(redis_cmds, "\n", absl::SkipWhitespace());

  for (auto e : redis_cmd_set) {
    PL_LOG_VAR(e);
  }
  std::vector<std::string_view> lines = absl::StrSplit(redis_cmdargs, "\n", absl::SkipWhitespace());

  std::vector<std::string_view> output_line;

  for (std::string_view line : lines) {
    constexpr std::string_view kWhiteSpaces = " \t\v\n";
    absl::ConsumePrefix(&line, kWhiteSpaces);
    absl::ConsumeSuffix(&line, kWhiteSpaces);

    if (redis_cmd_set.contains(line) && !output_line.empty()) {
      commands->push_back(std::move(output_line));
    }
    output_line.push_back(line);
  }

  if (!output_line.empty()) {
    commands->push_back(std::move(output_line));
  }
  return Status::OK();
}

std::string FomatCommand(const std::vector<std::string_view>& command) {
  std::vector<std::string> args;

  for (const auto c : VectorView<std::string_view>(command, 1, command.size() - 1)) {
    if (absl::StrContains(c, "\"")) {
      args.push_back(absl::Substitute("R\"($0)\"", c));
    } else {
      args.push_back(absl::Substitute(R"("$0")", c));
    }
  }

  std::string args_string = absl::Substitute(R"({$0})", absl::StrJoin(args, ", "));

  return absl::Substitute(R"({"$0", $1},)", command.front(), args_string);
}

// Prints a map from redis command name to its arguments names.
int main(int argc, char* argv[]) {
  pl::EnvironmentGuard env_guard(&argc, argv);

  CHECK(!FLAGS_redis_cmdargs.empty()) << "--redis_cmdargs must be specified.";
  CHECK(!FLAGS_redis_cmds.empty()) << "--redis_cmds must be specified.";

  PL_ASSIGN_OR_EXIT(std::string redis_cmdargs, ReadFileToString(FLAGS_redis_cmdargs));
  PL_ASSIGN_OR_EXIT(std::string redis_cmds, ReadFileToString(FLAGS_redis_cmds));

  std::vector<std::vector<std::string_view>> commands;

  PL_CHECK_OK(Main(redis_cmds, redis_cmdargs, &commands));

  for (const auto& command : commands) {
    std::cout << FomatCommand(command) << std::endl;
  }

  return 0;
}