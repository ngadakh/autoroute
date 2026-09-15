#!/bin/bash
# Regenerates docs/demo.gif.
#
# Primary path: `vhs docs/demo.tape` (see that file). vhs drives a headless
# Chrome via go-rod to screenshot terminal frames, which some sandboxed /
# browser-less environments can't do — it fails silently (exit 0, no error,
# no output file) rather than reporting the problem. If `vhs docs/demo.tape`
# produces no docs/demo.gif, use this script instead: it records the same
# three-prompt, three-tier demo with asciinema (a plain PTY recorder, no
# browser involved) and rasterizes it to GIF with agg.
#
#   brew install asciinema agg
#   make setup   # so L2 routing has the real embedder, not just L1 + default
#   scripts/record-demo.sh
set -euo pipefail
cd "$(dirname "$0")/.."

if lsof -i :8080 -sTCP:LISTEN >/dev/null 2>&1; then
  echo "port 8080 is already in use — stop whatever's listening and retry" >&2
  exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

cat > "$work/run.sh" <<'INNER'
#!/bin/bash
set -u
cd "__REPO_ROOT__"

make run > /tmp/autoroute-demo.log 2>&1 &
RUN_PID=$!
sleep 2
clear

q() {
  echo "\$ $1"
  eval "$1"
  echo
}

q "curl -s localhost:8080/v1/chat/completions -d '{\"model\":\"auto\",\"messages\":[{\"role\":\"user\",\"content\":\"Who is the prime minister of India?\"}]}' | jq '{model, answer: .choices[0].message.content}'"
sleep 2

q "curl -s localhost:8080/v1/chat/completions -d '{\"model\":\"auto\",\"messages\":[{\"role\":\"user\",\"content\":\"Add type hints to this function: def f(x): return x*2\"}]}' | jq '{model, answer: .choices[0].message.content}'"
sleep 2

q "curl -s localhost:8080/v1/chat/completions -d '{\"model\":\"auto\",\"messages\":[{\"role\":\"user\",\"content\":\"Critique the architecture of this system design and list the tradeoffs.\"}]}' | jq '{model, answer: .choices[0].message.content}'"
sleep 2

q "curl -s localhost:8080/metrics | grep autoroute_route_decisions_total"
sleep 2

kill "$RUN_PID" 2>/dev/null
wait "$RUN_PID" 2>/dev/null
INNER
sed -i.bak "s#__REPO_ROOT__#$(pwd)#" "$work/run.sh" && rm -f "$work/run.sh.bak"
chmod +x "$work/run.sh"

asciinema rec "$work/demo.cast" --overwrite --window-size 150x40 -c "bash $work/run.sh"
agg --theme dracula --font-size 16 "$work/demo.cast" docs/demo.gif

echo "wrote docs/demo.gif"
