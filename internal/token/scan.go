package token

import "regexp"

// ScanPattern matches virtual keys and child keys in text, for kman scan and
// for third-party secret scanners (ADR 0001). It is published: changing it
// breaks every scanner that copied it. A match is confirmed with Parse.
// The (?:ek_)? is needed because _ is a word character: \bkmn_ alone would
// miss child keys.
const ScanPattern = `\b(?:ek_)?kmn_[0-9A-Za-z]{8}_[0-9A-Za-z]{49}\b`

// Scanner is ScanPattern compiled.
var Scanner = regexp.MustCompile(ScanPattern)
