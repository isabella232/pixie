#ifdef __linux__

#include "src/stirling/bpf_tools/bpftrace_wrapper.h"

#include "src/common/base/base.h"

#include "third_party/bpftrace/src/ast/codegen_llvm.h"
#include "third_party/bpftrace/src/ast/semantic_analyser.h"
#include "third_party/bpftrace/src/clang_parser.h"
#include "third_party/bpftrace/src/driver.h"
#include "third_party/bpftrace/src/procmon.h"
#include "third_party/bpftrace/src/tracepoint_format_parser.h"

#include "src/stirling/utils/linux_headers.h"

namespace pl {
namespace stirling {
namespace bpf_tools {

Status BPFTraceWrapper::Compile(std::string_view script, const std::vector<std::string>& params) {
  int err;
  int success;
  bpftrace::Driver driver(bpftrace_);

  // Change these values for debug
  // bpftrace::bt_verbose = true;
  // bpftrace::bt_debug = bpftrace::DebugLevel::kFullDebug;

  // Script from string (command line argument)
  err = driver.parse_str(std::string(script));
  if (err != 0) {
    return error::Internal("Could not load bpftrace script.");
  }

  // Use this to pass parameters to bpftrace script ($1, $2 in the script)
  for (const auto& param : params) {
    bpftrace_.add_param(param);
  }

  // Appears to be required for printfs in bt file, so keep them.
  bpftrace_.join_argnum_ = 16;
  bpftrace_.join_argsize_ = 1024;

  err = static_cast<int>(!bpftrace::TracepointFormatParser::parse(driver.root_.get(), bpftrace_));
  if (err != 0) {
    return error::Internal("TracepointFormatParser failed.");
  }

  PL_ASSIGN_OR_RETURN(std::filesystem::path sys_headers_dir,
                      utils::FindOrInstallLinuxHeaders({utils::kDefaultHeaderSearchOrder}));
  LOG(INFO) << absl::Substitute("Using linux headers found at $0 for BPFtrace runtime.",
                                sys_headers_dir.string());

  // TODO(oazizi): Include dirs and include files not used right now.
  //               Consider either removing them or pushing them up into the Deploy() interface.
  std::vector<std::string> include_dirs;
  std::vector<std::string> include_files;
  std::vector<std::string> extra_flags;
  {
    struct utsname utsname;
    uname(&utsname);
    std::string ksrc, kobj;
    auto kdirs = bpftrace::get_kernel_dirs(utsname);
    ksrc = std::get<0>(kdirs);
    kobj = std::get<1>(kdirs);

    if (ksrc != "") {
      extra_flags = bpftrace::get_kernel_cflags(utsname.machine, ksrc, kobj);
    }
  }
  extra_flags.push_back("-include");
  extra_flags.push_back(CLANG_WORKAROUNDS_H);

  for (auto dir : include_dirs) {
    extra_flags.push_back("-I");
    extra_flags.push_back(dir);
  }
  for (auto file : include_files) {
    extra_flags.push_back("-include");
    extra_flags.push_back(file);
  }

  bpftrace::ClangParser clang;
  success = clang.parse(driver.root_.get(), bpftrace_, extra_flags);
  if (!success) {
    return error::Internal("Clang parse failed.");
  }

  bpftrace::ast::SemanticAnalyser semantics(driver.root_.get(), bpftrace_, bpftrace_.feature_);
  err = semantics.analyse();
  if (err != 0) {
    return error::Internal("Semantic analyser failed.");
  }

  err = semantics.create_maps(bpftrace::bt_debug != bpftrace::DebugLevel::kNone);
  if (err != 0) {
    return error::Internal("Failed to create BPF maps");
  }

  bpftrace::ast::CodegenLLVM llvm(driver.root_.get(), bpftrace_);
  bpforc_ = llvm.compile();
  bpftrace_.bpforc_ = bpforc_.get();

  if (bpftrace_.num_probes() == 0) {
    return error::Internal("No bpftrace probes to deploy.");
  }

  return Status::OK();
}

Status BPFTraceWrapper::Deploy(const PrintfCallback& printf_callback) {
  if (!IsRoot()) {
    return error::PermissionDenied("Bpftrace currently only supported as the root user.");
  }

  if (printf_callback) {
    bpftrace_.printf_callback_ = printf_callback;
  }

  int err = bpftrace_.deploy();
  if (err != 0) {
    return error::Internal("Failed to run BPF code.");
  }
  return Status::OK();
}

void BPFTraceWrapper::PollPerfBuffers(int timeout_ms) {
  bpftrace_.poll_perf_events(/* drain */ false, timeout_ms);
}

void BPFTraceWrapper::Stop() {
  // There is no need to manually cleanup bpftrace_.
}

StatusOr<std::vector<bpftrace::Field>> BPFTraceWrapper::OutputFields() {
  if (bpftrace_.printf_args_.size() != 1) {
    return error::Internal(
        "The BPFTrace program must contain exactly one printf statement, but found $0.",
        bpftrace_.printf_args_.size());
  }

  return std::get<1>(bpftrace_.printf_args_.front());
}

bpftrace::BPFTraceMap BPFTraceWrapper::GetBPFMap(const std::string& name) {
  return bpftrace_.get_map(name);
}

}  // namespace bpf_tools
}  // namespace stirling
}  // namespace pl

#endif