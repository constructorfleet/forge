#!/usr/bin/env bash
#
# forge-afk-dag.sh — run `forge execute <n>` over the actionable OPEN issues in
# the GitHub issue tracker, in dependency order, where a ticket that depends on
# another does NOT start until its dependency's PR is MERGED into the base
# branch (origin/main by default).
#
# Why a wrapper and not just `forge execute a b c`:
#   `forge execute` resolves the DAG internally, but for prerequisites that are
#   part of the SAME run it advances on in-process completion (the agent
#   reaching IMPLEMENTED), not on a real merge — ticket 22's "PR merged" signal
#   isn't wired for managed prerequisites. Only *external* prerequisites are
#   gated on a merged-into-base PR. This script gives every ticket the stricter,
#   real-world gate: launch each ticket as its own `forge execute` invocation
#   and hold its dependents until the dependency actually lands on origin/main.
#
# Merge gate: a dependency D is "merged" when the GitHub issue #D is CLOSED as
# COMPLETED (i.e. closed by a merged PR). This mirrors forge's own external
# dependency-satisfaction semantics (ADR 0005 / ticket 06: "PR merged into the
# base branch"). Requires the `gh` CLI, unless you pass --no-merge-gate.
#
# Ticket source: OPEN issues in the GitHub tracker (via `gh issue list`), which
# is the same tracker `forge execute <issue-number>` operates on — so the issue
# number, its dependencies, and the merge gate all key on one GitHub number.
#
#   Status: from the issue's triage labels, falling back to a "Status:" line in
#           the body (actionable == matches --status, default ready-for-agent).
#   Dependencies: from a "Blocked by:" / "Depends on:" line, or a
#           "## Dependencies" block containing "- #NN". Any integers found are
#           deps (interpreted as GitHub issue numbers).
#
# Usage:
#   scripts/forge-afk-dag.sh [options]
#
# Options:
#   -R, --repo OWNER/NAME  GitHub repo to read (default: current dir's remote)
#   -l, --label LABEL      Only consider open issues carrying this label (repeatable)
#   -s, --status REGEX     Actionable Status regex (default: ready-for-agent)
#   -b, --base REF         Base branch deps must merge into (default: origin/main)
#   -p, --parallel N       Max concurrent `forge execute` runs (default: 1)
#   -i, --interval SEC     Merge-poll interval (default: 30)
#   -t, --timeout SEC      Max seconds to wait for a single merge, 0=inf (default: 0)
#       --forge-arg ARG    Extra arg passed through to `forge execute` (repeatable)
#       --no-merge-gate    Unblock dependents on execute completion, not on merge
#   -n, --dry-run          Print the plan and the commands; execute nothing
#   -h, --help             This help
#
# Exit codes: 0 all done | 1 a ticket errored | 2 cycle / stall / bad usage
#
# Requires bash 3.2+ (state is kept in a temp dir, so no assoc-array dependency).
set -euo pipefail

# ---- defaults --------------------------------------------------------------
REPO=""
LABELS=()
STATUS_RE="ready-for-agent"
BASE="origin/main"
PARALLEL=1
INTERVAL=30
TIMEOUT=0
DRY_RUN=0
MERGE_GATE=1
FORGE_ARGS=()

# ---- args ------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    -R|--repo)         REPO="$2"; shift 2 ;;
    -l|--label)        LABELS+=("$2"); shift 2 ;;
    -s|--status)       STATUS_RE="$2"; shift 2 ;;
    -b|--base)         BASE="$2"; shift 2 ;;
    -p|--parallel)     PARALLEL="$2"; shift 2 ;;
    -i|--interval)     INTERVAL="$2"; shift 2 ;;
    -t|--timeout)      TIMEOUT="$2"; shift 2 ;;
    --forge-arg)       FORGE_ARGS+=("$2"); shift 2 ;;
    --no-merge-gate)   MERGE_GATE=0; shift ;;
    -n|--dry-run)      DRY_RUN=1; shift ;;
    -h|--help)         sed -n '2,60p' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

log()  { printf '%s %s\n' "$(date '+%H:%M:%S')" "$*" >&2; }
die()  { log "ERROR: $*"; exit 2; }

command -v forge >/dev/null 2>&1 || die "forge not on PATH (build it: go build -o \$GOBIN/forge ./cmd/forge)"
command -v gh   >/dev/null 2>&1 || die "gh CLI required to read the issue tracker; install it (https://cli.github.com)"
command -v jq   >/dev/null 2>&1 || die "jq required to parse the issue tracker JSON"

# gh with the optional --repo scope threaded through every call.
GH_REPO_ARGS=(); [[ -n "$REPO" ]] && GH_REPO_ARGS=(--repo "$REPO")
gh_() { gh "$@" ${GH_REPO_ARGS[@]+"${GH_REPO_ARGS[@]}"}; }

# ---- file-backed maps (bash 3.2 has no associative arrays) ------------------
WORK="$(mktemp -d "${TMPDIR:-/tmp}/forge-afk.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/deps" "$WORK/title" "$WORK/status" "$WORK/state" "$WORK/pid" "$WORK/start" "$WORK/seen" "$WORK/indeg"
setf() { printf '%s' "$3" >"$WORK/$1/$2"; }                 # setf MAP KEY VALUE
getf() { cat "$WORK/$1/$2" 2>/dev/null || true; }          # getf MAP KEY
hasf() { [[ -e "$WORK/$1/$2" ]]; }                          # hasf MAP KEY

ACTIONABLE=()          # issue numbers we intend to run, in tracker order

# parse_status BODY_FILE LABELS_CSV -> the triage status: prefer labels (any
# label matching the status regex wins), fall back to a body "Status:" line.
parse_status() {
  local f="$1" labels="$2"
  if [[ -n "$labels" ]]; then printf '%s' "$labels"; return; fi
  grep -iEm1 '^[*_ ]*status[*_ ]*:' "$f" 2>/dev/null | sed -E 's/.*:[[:space:]]*//; s/[[:space:]]*$//' | tr -d '*_'
}

parse_deps() { # space-separated dependency issue numbers found in body $1 (self=$2 dropped)
  local f="$1" self="$2"
  {
    grep -iE '(blocked by|depends on|prerequisite)s?[*_ ]*:' "$f" 2>/dev/null || true
    awk 'tolower($0) ~ /^#+[[:space:]]*dependenc/ {grab=1; next}
         /^#/ && grab {grab=0}
         grab' "$f"
  } | grep -oE '#?[0-9]+' | tr -d '#' | sort -un | while read -r d; do
        [[ -n "$d" && "$((10#$d))" -ne "$self" ]] && echo "$((10#$d))"
      done | paste -sd' ' -
}

# Pull OPEN issues (optionally filtered by --label) from the GitHub tracker.
LABEL_ARGS=()
if [[ ${#LABELS[@]} -gt 0 ]]; then
  for l in "${LABELS[@]}"; do LABEL_ARGS+=(--label "$l"); done
  LABELS_NOTE=" labeled: ${LABELS[*]}"
else
  LABELS_NOTE=""
fi
log "reading open issues from GitHub tracker${REPO:+ ($REPO)}${LABELS_NOTE}"
ISSUES_JSON="$WORK/issues.json"
gh_ issue list --state open ${LABEL_ARGS[@]+"${LABEL_ARGS[@]}"} --limit 1000 \
    --json number,title,body,labels >"$ISSUES_JSON" 2>"$WORK/gh.err" \
  || die "gh issue list failed: $(cat "$WORK/gh.err")"

# number<TAB>title<TAB>labels-csv, one issue per line; body written per-issue.
mkdir -p "$WORK/body"
count=0
while IFS=$'\t' read -r n title labels; do
  [[ -n "$n" ]] || continue
  n="$((10#$n))"
  jq -r --argjson num "$n" '.[] | select(.number==$num) | .body' "$ISSUES_JSON" >"$WORK/body/$n"
  setf seen   "$n" 1
  setf title  "$n" "$title"
  setf status "$n" "$(parse_status "$WORK/body/$n" "$labels")"
  setf deps   "$n" "$(parse_deps  "$WORK/body/$n" "$n")"
  count=$((count+1))
  if [[ "$(getf status "$n")" =~ $STATUS_RE ]]; then
    ACTIONABLE+=("$n")
  fi
done < <(jq -r '.[] | [.number, .title, ([.labels[].name] | join(","))] | @tsv' "$ISSUES_JSON")

[[ $count -gt 0 ]] || die "no open issues found in the GitHub tracker${REPO:+ ($REPO)}"

[[ ${#ACTIONABLE[@]} -gt 0 ]] || { log "no actionable tickets (Status ~ /$STATUS_RE/) — nothing to do"; exit 0; }

in_runset() { local x="$1" a; for a in "${ACTIONABLE[@]}"; do [[ "$a" == "$x" ]] && return 0; done; return 1; }

# ---- cycle detection over the run-set (Kahn) -------------------------------
# Only edges among ACTIONABLE tickets can deadlock this run; external deps are
# resolved by the merge gate, not by us, so they can't form a cycle here.
for n in "${ACTIONABLE[@]}"; do
  deg=0; for d in $(getf deps "$n"); do in_runset "$d" && deg=$((deg+1)); done
  setf indeg "$n" "$deg"
done
queue=(); for n in "${ACTIONABLE[@]}"; do [[ "$(getf indeg "$n")" -eq 0 ]] && queue+=("$n"); done
processed=0
while [[ ${#queue[@]} -gt 0 ]]; do
  cur="${queue[0]}"; queue=("${queue[@]:1}"); processed=$((processed+1))
  for m in "${ACTIONABLE[@]}"; do
    for d in $(getf deps "$m"); do
      if [[ "$d" == "$cur" ]]; then
        nd=$(( $(getf indeg "$m") - 1 )); setf indeg "$m" "$nd"
        [[ "$nd" -eq 0 ]] && queue+=("$m")
      fi
    done
  done
done
[[ $processed -eq ${#ACTIONABLE[@]} ]] || die "dependency cycle among actionable tickets — cannot order"

# ---- merge gate ------------------------------------------------------------
# is_merged N -> 0 if dependency #N is satisfied (landed on base), else 1.
is_merged() {
  local n="$1"
  [[ "$MERGE_GATE" -eq 0 ]] && return 1
  # A dep outside our run-set that the tracker already marks resolved/done is
  # treated as landed — don't wait forever on already-shipped work.
  if ! in_runset "$n" && hasf seen "$n"; then
    case "$(getf status "$n" | tr 'A-Z' 'a-z')" in
      *resolved*|*done*|*merged*|*closed*) return 0 ;;
    esac
  fi
  # Authoritative signal: issue closed as completed == closed by a merged PR.
  local state reason
  state="$(gh_ issue view "$n" --json state -q .state 2>/dev/null || true)"
  reason="$(gh_ issue view "$n" --json stateReason -q .stateReason 2>/dev/null || true)"
  [[ "$state" == "CLOSED" && "$reason" == "COMPLETED" ]]
}

# ---- orchestration loop ----------------------------------------------------
for n in "${ACTIONABLE[@]}"; do setf state "$n" pending; done

deps_satisfied() { # 0 if every dependency of $1 has landed
  local n="$1" d
  for d in $(getf deps "$n"); do
    if in_runset "$d"; then
      [[ "$(getf state "$d")" == "done" ]] || return 1     # run-set dep must be fully merged
    else
      is_merged "$d" || return 1                            # external dep must be merged
    fi
  done
  return 0
}

running_count() { local c=0 n; for n in "${ACTIONABLE[@]}"; do [[ "$(getf state "$n")" == "running" ]] && c=$((c+1)); done; echo "$c"; }

launch() {
  local n="$1"
  log "> launching forge execute $n  ($(getf title "$n"))"
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "DRY-RUN: forge execute ${FORGE_ARGS[*]:-} $n"
    setf state "$n" done            # pretend it merged so the plan drains
    return
  fi
  local logf="forge-exec-${n}.log"
  ( forge execute ${FORGE_ARGS[@]+"${FORGE_ARGS[@]}"} "$n" ) >"$logf" 2>&1 &
  setf pid "$n" "$!"
  setf start "$n" "$(date +%s)"
  setf state "$n" running
  log "  pid $(getf pid "$n"), output -> $logf"
}

log "plan: ${#ACTIONABLE[@]} actionable ticket(s): ${ACTIONABLE[*]}"
for n in "${ACTIONABLE[@]}"; do
  dd="$(getf deps "$n")"; [[ -n "$dd" ]] && log "  #$n depends on: $dd"
done
[[ "$DRY_RUN" -eq 1 ]] && log "(dry run — no forge execute, no merge polling)"

EXIT=0
while :; do
  remaining=0
  for n in "${ACTIONABLE[@]}"; do
    s="$(getf state "$n")"; [[ "$s" == "pending" || "$s" == "running" ]] && remaining=$((remaining+1))
  done
  [[ $remaining -eq 0 ]] && break

  progressed=0

  # 1) Reap finished forge execute processes and check for merges.
  for n in "${ACTIONABLE[@]}"; do
    [[ "$(getf state "$n")" == "running" ]] || continue
    pid="$(getf pid "$n")"
    if [[ -n "$pid" ]] && ! kill -0 "$pid" 2>/dev/null; then
      set +e; wait "$pid"; rc=$?; set -e
      : >"$WORK/pid/$n"; setf pid "$n" ""
      if [[ $rc -ne 0 ]]; then
        log "x forge execute $n exited $rc — see forge-exec-${n}.log"
        setf state "$n" error; EXIT=1; progressed=1; continue
      fi
      log "  forge execute $n finished (PR should now be open); waiting for merge into $BASE"
    fi
    if [[ -z "$(getf pid "$n")" ]]; then
      if [[ "$MERGE_GATE" -eq 0 ]] || is_merged "$n"; then
        setf state "$n" done; progressed=1
        if [[ "$MERGE_GATE" -eq 1 ]]; then log "ok #$n merged into $BASE"; else log "ok #$n done"; fi
      elif [[ "$TIMEOUT" -gt 0 ]]; then
        now=$(date +%s); st="$(getf start "$n")"
        if (( now - ${st:-now} > TIMEOUT )); then
          log "x #$n not merged within ${TIMEOUT}s — giving up"
          setf state "$n" error; EXIT=1; progressed=1
        fi
      fi
    fi
  done

  # 2) Launch newly-unblocked tickets up to the parallelism cap.
  for n in "${ACTIONABLE[@]}"; do
    [[ "$(getf state "$n")" == "pending" ]] || continue
    [[ "$(running_count)" -lt "$PARALLEL" ]] || break
    if deps_satisfied "$n"; then launch "$n"; progressed=1; fi
  done

  # 3) Deadlock check: nothing running, nothing launchable, work remains.
  if [[ "$(running_count)" -eq 0 && $progressed -eq 0 ]]; then
    stuck=1
    for n in "${ACTIONABLE[@]}"; do
      [[ "$(getf state "$n")" == "pending" ]] || continue
      if deps_satisfied "$n"; then stuck=0; break; fi
    done
    if [[ $stuck -eq 1 ]]; then
      log "stalled: pending tickets whose dependencies are not (and won't become) satisfied:"
      for n in "${ACTIONABLE[@]}"; do
        [[ "$(getf state "$n")" == "pending" ]] && log "  #$n blocked on: $(getf deps "$n")"
      done
      exit 2
    fi
  fi

  [[ "$DRY_RUN" -eq 1 ]] && continue          # dry run drains instantly
  [[ $progressed -eq 1 ]] || { git fetch --quiet origin 2>/dev/null || true; sleep "$INTERVAL"; }
done

log "done. final states:"
for n in "${ACTIONABLE[@]}"; do log "  #$n -> $(getf state "$n")"; done
exit "$EXIT"
