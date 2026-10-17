#!/bin/zsh
# Double-click to open Ferry. It normally runs on its own from login (see README); if it isn't
# running, this starts it here: close this window (or press Ctrl-C) to stop it.
cd "${0:A:h}" || exit 1
if curl -s -o /dev/null --max-time 2 http://localhost:7070/; then
  open http://localhost:7070
  exit 0
fi
[[ -x ./ferry ]] || go build -o ferry . || exit 1
exec ./ferry
