#!/bin/sh
set -eu

verifier_user=micro1verifier

random_token() {
  od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

compile() {
  module=$1
  package_path=$2
  package_name=$3
  binary=$4
  state=${5:-}
  proof_file=

  if test -n "$state"; then
    mkdir -p "$state"
    chmod 0700 "$state"
    token=$(random_token)
    printf '%s\n' "$token" > "$state/token"
    chmod 0600 "$state/token"
    proof_file="$module/$package_path/zzzz_micro1_verifier_proof_test.go"
    printf 'package %s\n\nimport (\n\t"os"\n\t"testing"\n)\n\nfunc TestZZZZMicro1VerifierProof(t *testing.T) {\n\tf := os.NewFile(3, "verifier-proof")\n\tif f == nil {\n\t\tt.Fatal("verifier proof channel unavailable")\n\t}\n\tif _, err := f.WriteString("%s\\n"); err != nil {\n\t\tt.Fatal(err)\n\t}\n}\n' "$package_name" "$token" > "$proof_file"
    chmod 0400 "$proof_file"
  fi

	go_flags=${MICRO1_GOFLAGS:--mod=mod -p=1}
	export GOMAXPROCS=${MICRO1_GOMAXPROCS:-2}
	if (cd "$module" && CGO_ENABLED=0 GOWORK=off GOFLAGS="$go_flags" go test -c -o "$binary" "./$package_path"); then
    compile_status=0
  else
    compile_status=$?
  fi
  if test -n "$proof_file"; then
    rm -f "$proof_file"
  fi
  test "$compile_status" -eq 0 || return "$compile_status"
  # Keep the binary root-owned. If the candidate UID owned it, that process
  # could chmod the execute-only file and recover verifier-linked code.
  chown root:root "$binary"
  chmod 0111 "$binary"
}

compile_program() {
  module=$1
  package_path=$2
  binary=$3

	go_flags=${MICRO1_GOFLAGS:--mod=mod -p=1}
	export GOMAXPROCS=${MICRO1_GOMAXPROCS:-2}
	if (cd "$module" && CGO_ENABLED=0 GOWORK=off GOFLAGS="$go_flags" go build \
      -trimpath -buildvcs=false -o "$binary" "$package_path"); then
    :
  else
    return $?
  fi
  chown root:root "$binary"
  chmod 0111 "$binary"
}

compile_controller() {
  source_path=$1
  binary=$2

  controller_build=$(mktemp -d /tmp/micro1-controller-build.XXXXXX)
  cp "$source_path" "$controller_build/main.go"
  if (cd "$controller_build" && GOWORK=off GO111MODULE=off go build \
      -trimpath -buildvcs=false -o "$binary" main.go); then
    build_status=0
  else
    build_status=$?
  fi
  rm -rf "$controller_build"
  test "$build_status" -eq 0 || return "$build_status"
  chown root:root "$binary"
  chmod 0500 "$binary"
}

compile_trusted_controller() {
  module=$1
  source_path=$2
  binary=$3

  controller_build=$(mktemp -d "$module/micro1-controller.XXXXXX")
  cp "$source_path" "$controller_build/main.go"
  package_path=./$(basename "$controller_build")
	go_flags=${MICRO1_GOFLAGS:--mod=mod -p=1}
	export GOMAXPROCS=${MICRO1_GOMAXPROCS:-2}
	if (cd "$module" && CGO_ENABLED=0 GOWORK=off GOFLAGS="$go_flags" go build \
      -trimpath -buildvcs=false -o "$binary" "$package_path"); then
    build_status=0
  else
    build_status=$?
  fi
  rm -rf "$controller_build"
  test "$build_status" -eq 0 || return "$build_status"
  chown root:root "$binary"
  chmod 0500 "$binary"
}

copy_runtime_data() {
  module=$1
  runtime=$2
  shift 2
  mkdir -p "$runtime"
  for package_path in "$@"; do
    target_parent="$runtime/$package_path"
    mkdir -p "$target_parent"
    for data_name in testdata fixtures; do
      # Runtime fixtures are verifier-owned clean-parent data, not mutable
      # candidate files with trusted-looking names.
      source_path="/opt/reference/source/$package_path/$data_name"
      if test -d "$source_path"; then
        cp -R "$source_path" "$target_parent/$data_name"
      fi
    done
  done
  chown -R "$verifier_user:$verifier_user" "$runtime"
  chmod 0700 "$runtime"
}

seal() {
  module=$1
  runtime=$2
  shift 2
  copy_runtime_data "$module" "$runtime" "$@"

  # No candidate or injected verifier source remains available to executing
  # candidate code. Only explicitly copied runtime testdata survives.
  rm -rf "$(dirname "$module")"
  chmod 0700 /tests /opt/reference /logs /app/verifier-artifacts
  mkdir -p /tmp/micro1-controller-home /tmp/micro1-verifier-home /tmp/micro1-verifier-tmp
  chown root:root /tmp/micro1-controller-home
  chmod 0700 /tmp/micro1-controller-home
  chown "$verifier_user:$verifier_user" /tmp/micro1-verifier-home /tmp/micro1-verifier-tmp
  chmod 0700 /tmp/micro1-verifier-home /tmp/micro1-verifier-tmp
}

kill_candidate_processes() {
  for process_status in /proc/[0-9]*/status; do
    test -r "$process_status" || continue
    process_uid=$(awk '/^Uid:/ { print $2; exit }' "$process_status")
    test "$process_uid" = 65532 || continue
    process_id=${process_status#/proc/}
    process_id=${process_id%/status}
    kill -KILL "$process_id" 2>/dev/null || :
  done
}

run_binary() {
  binary=$1
  cwd=$2
  test_pattern=$3
  proof_state=${4:-}
  output=$(mktemp /tmp/micro1-verifier-output.XXXXXX)
  chmod 0600 "$output"

  if test -n "$proof_state"; then
    proof=$(mktemp /tmp/micro1-verifier-proof.XXXXXX)
    chmod 0600 "$proof"
    if /tests/common/run-candidate.sh "$binary" "$cwd" \
      -test.run "$test_pattern" -test.count=1 > "$output" 2>&1 3> "$proof"; then
      run_status=0
    else
      run_status=$?
    fi
    kill_candidate_processes
    expected=$(cat "$proof_state/token")
    observed=$(cat "$proof")
    rm -f "$proof" "$proof_state/token"
    rmdir "$proof_state"
    if test "$run_status" -eq 0 && test "$observed" = "$expected"; then
      rm -f "$output"
      return 0
    fi
  else
    if /tests/common/run-candidate.sh "$binary" "$cwd" \
      -test.run "$test_pattern" -test.count=1 > "$output" 2>&1; then
      kill_candidate_processes
      rm -f "$output"
      return 0
    fi
    kill_candidate_processes
  fi

  cat "$output"
  rm -f "$output"
  return 1
}

run_controller() {
  controller=$1
  cwd=$2
  shift 2
  output=$(mktemp /tmp/micro1-controller-output.XXXXXX)
  chmod 0600 "$output"

  if (cd "$cwd" && env -i \
      HOME=/tmp/micro1-controller-home TMPDIR=/tmp \
      PATH=/usr/local/go/bin:/usr/sbin:/usr/bin:/sbin:/bin \
      MICRO1_CANDIDATE_LAUNCHER=/tests/common/run-candidate.sh \
      "$controller" "$@" > "$output" 2>&1); then
    kill_candidate_processes
    rm -f "$output"
    return 0
  fi

  kill_candidate_processes
  cat "$output"
  rm -f "$output"
  return 1
}

command=$1
shift
case "$command" in
  compile) compile "$@" ;;
  compile-program) compile_program "$@" ;;
  compile-controller) compile_controller "$@" ;;
  compile-trusted-controller) compile_trusted_controller "$@" ;;
  seal) seal "$@" ;;
  run) run_binary "$@" ;;
  run-controller) run_controller "$@" ;;
  *) printf 'unknown hardened runner command: %s\n' "$command" >&2; exit 2 ;;
esac
