#!/usr/bin/env bash
#
# Drives the terminal recording for the README demo GIF. Use run.sh to record
# and render it; it calls the real TypeSafe API, so it needs an API key.
#
# The prompt is plain ASCII so the GIF renders with any monospace font; the
# palette is chosen at render time with `agg --theme`.
#
# Requires: hunch on PATH, bash.

set -euo pipefail

# Run from the fixtures directory so the commands in the GIF stay short.
cd "$(dirname "${BASH_SOURCE[0]}")/data"
rm -rf out

# --- colors -------------------------------------------------------------------
# 24-bit "R;G;B" values, so they look the same whatever `agg --theme` is used
# for the rest of the palette. Defaults follow GitHub's dark syntax colors.
PROMPT_DIR="${PROMPT_DIR:-121;192;255}"     # ~/hunch
PROMPT_SIGN="${PROMPT_SIGN:-210;168;255}"   # $
COMMENT="${COMMENT:-139;148;158}"           # # notes
CMD_HUNCH="${CMD_HUNCH:-126;231;135}"       # hunch (bold)
CMD_OTHER="${CMD_OTHER:-126;231;135}"       # wc, sort, head, ...
STRING="${STRING:-165;214;255}"             # "quoted" and 'quoted'
FLAG="${FLAG:-255;166;87}"                  # --flags
OPERATOR="${OPERATOR:-255;123;114}"         # | < > ;

esc=$'\033'
r="${esc}[0m"
fg() { printf '%s[38;2;%sm' "$esc" "$1"; }
c_comment="$(fg "$COMMENT")${esc}[3m"
c_hunch="${esc}[1m$(fg "$CMD_HUNCH")"
c_cmd="$(fg "$CMD_OTHER")"
c_str="$(fg "$STRING")"
c_flag="$(fg "$FLAG")"
c_op="$(fg "$OPERATOR")"
PROMPT="$(fg "$PROMPT_DIR")~/hunch${r} $(fg "$PROMPT_SIGN")\$${r} "

TYPE_DELAY=0.04   # per-keystroke typing speed
AFTER_CMD=2.2     # pause to read command output
AFTER_NOTE=1.2    # pause to read a comment

# type_chars prints text one character at a time in one color.
type_chars() {
  local text="$1" color="$2" i
  printf '%s' "$color"
  for (( i = 0; i < ${#text}; i++ )); do
    printf '%s' "${text:i:1}"
    sleep "$TYPE_DELAY"
  done
  printf '%s' "$r"
}

# type_cmd types a shell command with syntax highlighting: commands, quoted
# strings, flags and operators. A "\" + newline continues on an indented line.
type_cmd() {
  local s="$1" i=0 n=${#1} ch word end cmdpos=1
  while (( i < n )); do
    ch="${s:i:1}"
    case "$ch" in
      ' ')
        printf ' '; sleep "$TYPE_DELAY"; i=$((i + 1)); continue ;;
      $'\n')
        printf '\n    '; i=$((i + 1)); continue ;;
      '\')
        type_chars '\' ""; i=$((i + 1)); continue ;;
      '"'|"'")
        end="${s:i+1}"; end="${end%%"$ch"*}"
        type_chars "$ch$end$ch" "$c_str"
        i=$((i + ${#end} + 2)); cmdpos=0; continue ;;
      '|'|';'|'<'|'>')
        type_chars "$ch" "$c_op"
        i=$((i + 1))
        [[ "$ch" == '|' || "$ch" == ';' ]] && cmdpos=1
        continue ;;
    esac
    word="${s:i}"; word="${word%%[ $'\n'|;<>\"\']*}"
    if (( cmdpos )); then
      if [[ "$word" == hunch ]]; then type_chars "$word" "$c_hunch"; else type_chars "$word" "$c_cmd"; fi
      cmdpos=0
    elif [[ "$word" == -* ]]; then
      type_chars "$word" "$c_flag"
    else
      type_chars "$word" ""
    fi
    i=$((i + ${#word}))
  done
}

# Print a command at the prompt as if typed, run it, then leave a blank line.
run() {
  printf '%s' "$PROMPT"
  type_cmd "$*"
  printf '\n'
  sleep 0.4
  eval "$*" || true
  printf '\n'
  sleep "$AFTER_CMD"
}

# Print a comment at the prompt (not executed).
note() {
  printf '%s' "$PROMPT"
  type_chars "# $*" "$c_comment"
  printf '\n'
  sleep "$AFTER_NOTE"
}

trap 'rm -rf out' EXIT

clear
note "hunch: ask a plain-English question about every line of input."
run 'hunch "Is this an authentication failure?" app.log'

note "Keep confident yeses and print just the original lines."
run 'hunch "Is this an authentication failure?" --min 0.8 --raw app.log'

note "Route JSON tickets into a file per team, judging only their .body."
run "hunch pick \"Which team?\" -c billing,technical,sales \\
--jsonpath '\$.body' -o 'out/{choice}.jsonl' < tickets.jsonl"
run "wc -l out/*"

note "Rank on a scale and keep the top 3."
run 'hunch score "How frustrated is the customer?" -l calm,annoyed,furious \
--top 3 reviews.txt'

note "Piped output is JSONL or TSV, ready for the next command."
run 'hunch "Should on-call look at this?" --tsv app.log | sort -rn | head -3'
sleep 1.5
