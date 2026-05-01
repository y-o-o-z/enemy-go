package main

import (
	"math/rand/v2"
	"strings"
)

// alphabets ported from the original C version (defs in command.c).
const (
	nickFirst = "`{}[]\\_^|ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	nickRest  = "`{}[]\\_^|ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-012345678"
	identAlph = "abcdefghijklmnopqrstuvwxyz"
)

// RandomNick returns a random IRC nickname between minLen and maxLen chars.
func RandomNick(minLen, maxLen int) string {
	n := minLen + rand.IntN(maxLen-minLen+1)
	var b strings.Builder
	b.WriteByte(nickFirst[rand.IntN(len(nickFirst))])
	for i := 1; i < n; i++ {
		b.WriteByte(nickRest[rand.IntN(len(nickRest))])
	}
	return b.String()
}

// RandomIdent returns a random IRC username/ident (lowercase letters).
func RandomIdent(minLen, maxLen int) string {
	n := minLen + rand.IntN(maxLen-minLen+1)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(identAlph[rand.IntN(len(identAlph))])
	}
	return b.String()
}

// PickString returns a uniformly chosen element of s, or "" if empty.
func PickString(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[rand.IntN(len(s))]
}
