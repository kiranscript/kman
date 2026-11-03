#!/bin/bash
# THROWAWAY checks for kman's macOS keychain key source. Never prints the secret.
set -u
D=$(cd "$(dirname "$0")" && pwd)
to() { perl -e 'alarm shift; exec @ARGV' "$@"; }
openssl rand -hex 32 | tr -d '\n' > /tmp/kman-expected; chmod 644 /tmp/kman-expected
S=$(cat /tmp/kman-expected)
echo "| check | result |" >> "$GITHUB_STEP_SUMMARY"; echo "|---|---|" >> "$GITHUB_STEP_SUMMARY"
res() { echo "RESULT | $1 | $2"; echo "| $1 | $2 |" >> "$GITHUB_STEP_SUMMARY"; }
cls() { # $1 output, $2 rc
  case "$1" in
    "$S") echo "SILENT READ: secret returned, no prompt" ;;
    *"$S"*) echo "SILENT READ: secret returned (with extra output)" ;;
    *SILENT_MATCH*) echo "SILENT READ: secret returned, no prompt" ;;
    *) if [ "$2" = 142 ]; then echo "BLOCKED: waited for a prompt, killed after 20 s"; else echo "REFUSED rc=$2: $(echo "$1" | tr '\n' ' ' | head -c 160)"; fi ;;
  esac; }
sec_read() { out=$(to 20 /usr/bin/security find-generic-password -s "$1" -a kman -w ${2:-} 2>&1); rc=$?; cls "$out" $rc; }
py_read() { out=$(to 20 python3 "$D/read.py" "$1" "$2" 2>&1); rc=$?; cls "$out" $rc; }

# A dedicated, unlocked keychain made the default (the runner's login keychain password is unknown).
security create-keychain -p testpw kman-test.keychain
security set-keychain-settings kman-test.keychain
security unlock-keychain -p testpw kman-test.keychain
security list-keychains -d user -s kman-test.keychain $(security list-keychains -d user | tr -d '"')
security default-keychain -s kman-test.keychain
KC=$(security default-keychain | tr -d ' "')
echo "default keychain: $KC"

# Item A: created exactly as the checklist and go-keyring (CGO off) do.
security add-generic-password -a kman -s kman-master-key -w "$S" kman-test.keychain
# Item B: created with an empty trusted-application list (-T "").
security add-generic-password -a kman -s kman-master-key-noacl -w "$S" -T "" kman-test.keychain

echo "--- ACLs (no secret data)"
security dump-keychain -a kman-test.keychain | grep -v -F "$S" | sed -n '1,200p'

res "A: /usr/bin/security, same user, another process" "$(sec_read kman-master-key)"
res "B (-T \"\"): /usr/bin/security, same user" "$(sec_read kman-master-key-noacl)"
res "A: python3 via Security.framework, interaction disallowed" "$(py_read kman-master-key deny)"
res "A: python3 via Security.framework, interaction allowed" "$(py_read kman-master-key allow)"
res "B (-T \"\"): python3, interaction disallowed" "$(py_read kman-master-key-noacl deny)"

# zalando/go-keyring, CGO_ENABLED=0
( cd "$D/gokr" && go mod init gokr >/dev/null 2>&1; go get github.com/zalando/go-keyring@latest >/dev/null 2>&1; CGO_ENABLED=0 go build -o /tmp/gokr . ) \
  && { res "A: go-keyring, CGO_ENABLED=0" "$(o=$(to 20 /tmp/gokr kman-master-key 2>&1); cls "$o" $?)"; \
       res "B (-T \"\"): go-keyring, CGO_ENABLED=0" "$(o=$(to 20 /tmp/gokr kman-master-key-noacl 2>&1); cls "$o" $?)"; } \
  || res "go-keyring" "build failed"
echo "go-keyring version: $(cd "$D/gokr" && go list -m github.com/zalando/go-keyring 2>/dev/null)"

# launchd: LaunchAgent in the user's GUI domain
chmod +x "$D/read.sh"
cat > /tmp/dev.kman.agent.plist <<P
<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>dev.kman.agent</string>
<key>ProgramArguments</key><array><string>/bin/sh</string><string>$D/read.sh</string><string>/tmp/agent.out</string></array>
<key>RunAtLoad</key><true/></dict></plist>
P
cp /tmp/dev.kman.agent.plist ~/Library/LaunchAgents/ 2>/dev/null || { mkdir -p ~/Library/LaunchAgents && cp /tmp/dev.kman.agent.plist ~/Library/LaunchAgents/; }
if launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.kman.agent.plist 2>/tmp/agent.err; then
  sleep 12; res "A: LaunchAgent (gui/$(id -u))" "$(tr '\n' ';' < /tmp/agent.out 2>/dev/null)"
  launchctl bootout gui/$(id -u)/dev.kman.agent 2>/dev/null
else res "A: LaunchAgent" "bootstrap failed: $(cat /tmp/agent.err)"; fi

# launchd: LaunchDaemon (root, system domain), default search list and explicit keychain path
for mode in default explicit; do
  arg=""; [ $mode = explicit ] && arg="$KC"
  sudo tee /Library/LaunchDaemons/dev.kman.daemon.plist >/dev/null <<P
<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>dev.kman.daemon</string>
<key>ProgramArguments</key><array><string>/bin/sh</string><string>$D/read.sh</string><string>/tmp/daemon-$mode.out</string><string>$arg</string></array>
<key>RunAtLoad</key><true/></dict></plist>
P
  if sudo launchctl bootstrap system /Library/LaunchDaemons/dev.kman.daemon.plist 2>/tmp/daemon.err; then
    sleep 12; res "A: LaunchDaemon as root ($mode keychain)" "$(tr '\n' ';' < /tmp/daemon-$mode.out 2>/dev/null)"
    sudo launchctl bootout system/dev.kman.daemon 2>/dev/null
  else res "A: LaunchDaemon ($mode)" "bootstrap failed: $(cat /tmp/daemon.err)"; fi
done

# Locked keychain
security lock-keychain kman-test.keychain
res "A, keychain locked: /usr/bin/security" "$(sec_read kman-master-key)"
res "A, keychain locked: python3, interaction disallowed" "$(py_read kman-master-key deny)"
security unlock-keychain -p testpw kman-test.keychain

# The real login keychain, if writable without a password
if security add-generic-password -a kman -s kman-login-test -w "$S" login.keychain 2>/tmp/login.err; then
  res "login keychain: /usr/bin/security" "$(out=$(to 20 /usr/bin/security find-generic-password -s kman-login-test -a kman -w login.keychain 2>&1); cls "$out" $?)"
else res "login keychain" "not writable: $(head -c 120 /tmp/login.err)"; fi
security delete-keychain kman-test.keychain
exit 0
