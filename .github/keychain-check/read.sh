#!/bin/sh
# Used from launchd. $1 = output file, $2 = optional keychain path.
{
  echo "uid=$(id -u) user=$(id -un)"
  v=$(/usr/bin/perl -e 'alarm shift; exec @ARGV' 20 /usr/bin/security find-generic-password -s kman-master-key -a kman -w $2 2>&1); rc=$?
  if [ "$v" = "$(cat /tmp/kman-expected)" ]; then echo "security-cli: SILENT_MATCH"; else
    case "$v" in *"$(cat /tmp/kman-expected)"*) echo "security-cli: returned secret with extra output";; *) echo "security-cli: rc=$rc $(echo "$v" | head -c 160)";; esac; fi
  echo "python: $(/usr/bin/perl -e 'alarm shift; exec @ARGV' 20 /usr/bin/python3 "$(dirname "$0")/read.py" kman-master-key deny 2>&1 | head -c 160)"
} > "$1" 2>&1
