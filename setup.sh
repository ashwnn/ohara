#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SKILLS_SOURCE="${REPO_ROOT}/skills"

if [ ! -d "${SKILLS_SOURCE}" ]; then
  echo "skills directory not found at ${SKILLS_SOURCE}" >&2
  exit 1
fi

link_skills() {
  local agent_dir="$1"
  local target_dir="${agent_dir}/skills"
  local source_path=""
  local skill_name=""
  local link_path=""

  mkdir -p "${target_dir}"

  for source_path in "${SKILLS_SOURCE}"/*; do
    skill_name="$(basename "${source_path}")"
    link_path="${target_dir}/${skill_name}"
    ln -sfn "${source_path}" "${link_path}"
    echo "linked ${link_path} -> ${source_path}"
  done
}

link_skills "${REPO_ROOT}/.opencode"

echo
echo "Done. Skills linked for project .opencode"
