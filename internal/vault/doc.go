// Package vault seals and unseals credentials with envelope encryption under the
// master key. It owns the wipeable plaintext cache, the four key-source
// adapters, and the offline rekey.
package vault
