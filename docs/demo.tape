# AutoRoute demo: three prompts, three tiers, routed automatically by
# real L1 heuristics + L2 embedding-classifier signals — not a hardcoded
# model name. Regenerate with `vhs docs/demo.tape` from the repo root
# (needs `make setup` run first so the real L2 embedder is available;
# otherwise everything still routes, just via L1 + the conservative
# default instead of a confident L2 decision).
#
# vhs drives a headless Chrome (via go-rod) to screenshot terminal frames;
# in an environment where that can't launch a browser, it fails silently —
# exit 0, no error, no output file. If that happens, use
# scripts/record-demo.sh instead: same demo, recorded with asciinema (a
# plain PTY recorder, no browser) and rasterized with agg.

Output docs/demo.gif

Require make
Require curl
Require jq

Set Shell "bash"
Set FontSize 20
Set Width 1200
Set Height 720
Set Theme "Dracula"
Set Padding 24
Set TypingSpeed 35ms

Hide
Type "make run > /tmp/autoroute-demo.log 2>&1 &"
Enter
Sleep 2s
Type "clear"
Enter
Show

Type `curl -s localhost:8080/v1/chat/completions -d '{"model":"auto","messages":[{"role":"user","content":"Who is the prime minister of India?"}]}' | jq '{model, answer: .choices[0].message.content}'`
Enter
Sleep 3s

Type `curl -s localhost:8080/v1/chat/completions -d '{"model":"auto","messages":[{"role":"user","content":"Add type hints to this function: def f(x): return x*2"}]}' | jq '{model, answer: .choices[0].message.content}'`
Enter
Sleep 3s

Type `curl -s localhost:8080/v1/chat/completions -d '{"model":"auto","messages":[{"role":"user","content":"Critique the architecture of this system design and list the tradeoffs."}]}' | jq '{model, answer: .choices[0].message.content}'`
Enter
Sleep 3s

Type "curl -s localhost:8080/metrics | grep autoroute_route_decisions_total"
Enter
Sleep 3s

Hide
Type "kill %1"
Enter
Show
Sleep 1s
