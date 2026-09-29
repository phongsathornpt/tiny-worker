package rest

import "strings"

// isEmail reports whether s is a plain addr-spec (local@domain) suitable for
// REST input validation.
//
// This is deliberately hand-rolled: net/mail would add ~55 KB gzip to every
// worker that imports this package. It accepts the forms API clients actually
// send and rejects obvious garbage, with two documented deviations from
// net/mail (pinned by the differential test):
//
//   - mailbox forms with display names, comments, or quoted local parts
//     ("Ada <ada@example.com>", `"a b"@example.com`) are rejected;
//   - the domain must be dotted with an alphabetic TLD of at least two
//     characters, so "a@localhost" and "a@b.c" are rejected.
func isEmail(s string) bool {
	if len(s) == 0 || len(s) > 254 {
		return false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at != strings.LastIndexByte(s, '@') {
		return false
	}
	local, domain := s[:at], s[at+1:]
	if len(local) > 64 || len(domain) == 0 || len(domain) > 253 {
		return false
	}
	return validEmailLocal(local) && validEmailDomain(domain)
}

// validEmailLocal checks a dot-atom local part: no leading/trailing dot, no
// consecutive dots, and only the RFC 5322 atext characters.
func validEmailLocal(local string) bool {
	if local == "" || local[0] == '.' || local[len(local)-1] == '.' {
		return false
	}
	prevDot := false
	for i := 0; i < len(local); i++ {
		c := local[i]
		if c == '.' {
			if prevDot {
				return false
			}
			prevDot = true
			continue
		}
		prevDot = false
		if !isAtext(c) {
			return false
		}
	}
	return true
}

func isAtext(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '/', '=', '?', '^', '_', '`', '{', '|', '}', '~':
		return true
	}
	return false
}

// validEmailDomain checks dot-separated labels: alphanumeric plus hyphens, no
// leading/trailing hyphen, at least one dot, alphabetic TLD of 2+ characters.
func validEmailDomain(domain string) bool {
	if domain == "" || domain[0] == '.' || domain[len(domain)-1] == '.' {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 {
		return false
	}
	for i := 0; i < len(tld); i++ {
		c := tld[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}
