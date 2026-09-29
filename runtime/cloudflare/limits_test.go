package cloudflare

import "testing"

func TestMaxBodyBytesDefault(t *testing.T) {
	if MaxBodyBytes <= 0 {
		t.Fatalf("MaxBodyBytes must default to a positive limit, got %d", MaxBodyBytes)
	}
	if MaxBodyBytes < 1<<20 {
		t.Fatalf("default MaxBodyBytes %d is suspiciously small (<1MiB)", MaxBodyBytes)
	}
}
