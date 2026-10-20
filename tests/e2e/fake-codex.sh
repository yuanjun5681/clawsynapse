#!/bin/sh
# Simulated Codex CLI exercising the supported JSONL contract. This is a
# controlled fixture: it never calls a model or network service.
case "$1" in
  login) echo 'Logged in'; exit 0 ;;
  --version) echo 'codex-cli e2e-fixture'; exit 0 ;;
esac
case "$*" in
  *--help*) echo 'exec resume --json --skip-git-repo-check stdin'; exit 0 ;;
esac

prompt=$(cat)
printf '%s\n' '{"type":"thread.started","thread_id":"e2e-thread"}' '{"type":"turn.started"}'

case "$prompt" in
  *smoke_unstructured*) echo 'Not logged in'; exit 0 ;;
esac

if printf '%s' "$prompt" | grep -q smoke_big; then
  big=$(head -c 300000 /dev/zero | tr '\0' x)
  printf '{"type":"item.completed","item":{"id":"big","type":"agent_message","text":"%s"}}\n' "$big"
  printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}'
  exit 0
fi

printf '%s\n' '{"type":"item.completed","item":{"id":"answer-1","type":"agent_message","text":"early e2e answer"}}'

case "$prompt" in
  *smoke_fail*) printf '%s\n' '{"type":"turn.failed","error":{"message":"failure after output"}}'; exit 0 ;;
  *smoke_hang*) sleep 30; exit 0 ;;
esac

# The delay lets the receiver prove the delta arrived before the terminal event.
sleep 2
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}'
