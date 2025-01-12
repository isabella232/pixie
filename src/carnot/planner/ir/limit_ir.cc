/*
 * Copyright 2018- The Pixie Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

#include "src/carnot/planner/ir/limit_ir.h"
#include "src/carnot/planner/ir/filter_ir.h"

namespace px {
namespace carnot {
namespace planner {

Status LimitIR::Init(OperatorIR* parent, int64_t limit_value, bool pem_only) {
  PL_RETURN_IF_ERROR(AddParent(parent));
  SetLimitValue(limit_value);
  pem_only_ = pem_only;
  return Status::OK();
}

StatusOr<std::vector<absl::flat_hash_set<std::string>>> LimitIR::RequiredInputColumns() const {
  DCHECK(IsRelationInit());
  return std::vector<absl::flat_hash_set<std::string>>{ColumnsFromRelation(relation())};
}

Status LimitIR::ToProto(planpb::Operator* op) const {
  auto pb = op->mutable_limit_op();
  op->set_op_type(planpb::LIMIT_OPERATOR);
  DCHECK_EQ(parents().size(), 1UL);

  auto parent_rel = parents()[0]->relation();
  auto parent_id = parents()[0]->id();

  for (const std::string& col_name : relation().col_names()) {
    planpb::Column* col_pb = pb->add_columns();
    col_pb->set_node(parent_id);
    col_pb->set_index(parent_rel.GetColumnIndex(col_name));
  }
  if (!limit_value_set_) {
    return CreateIRNodeError("Limit value not set properly.");
  }

  pb->set_limit(limit_value_);
  for (const auto src_id : abortable_srcs_) {
    pb->add_abortable_srcs(src_id);
  }
  return Status::OK();
}

Status LimitIR::CopyFromNodeImpl(const IRNode* node, absl::flat_hash_map<const IRNode*, IRNode*>*) {
  const LimitIR* limit = static_cast<const LimitIR*>(node);
  limit_value_ = limit->limit_value_;
  limit_value_set_ = limit->limit_value_set_;
  pem_only_ = limit->pem_only_;
  return Status::OK();
}

}  // namespace planner
}  // namespace carnot
}  // namespace px
