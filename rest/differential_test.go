package rest

import (
	"net/mail"
	"net/url"
	"sort"
	"testing"
)

// TestParseQueryDifferential pins the hand-rolled query parser to
// net/url.ParseQuery over the shapes the bridge can receive. net/url is
// test-only here: the shipped parser avoids it to keep the wasm lean.
func TestParseQueryDifferential(t *testing.T) {
	cases := []string{
		"",
		"a=1",
		"a=1&b=2",
		"a=1&&b=2&",
		"a",
		"a=",
		"=b",
		"name=Ada+Lovelace",
		"q=100%25",
		"path=a%2Fb",
		"utf8=%E6%97%A5%E6%9C%AC",
		"tag=a&tag=b&tag=",
		"limit=25&debug=true&rate=1.5",
		"key=value+with+spaces",
		"empty=&x=1",
		"a[]=1&a[]=2",
	}
	for _, raw := range cases {
		want, wantErr := url.ParseQuery(raw)
		got, ok := parseQuery(raw)
		if wantErr != nil {
			if ok {
				t.Errorf("parseQuery(%q) accepted a query net/url rejects", raw)
			}
			continue
		}
		if !ok {
			t.Errorf("parseQuery(%q) rejected a query net/url accepts", raw)
			continue
		}
		gotMap := map[string][]string{}
		for _, p := range got {
			gotMap[p.key] = append(gotMap[p.key], p.value)
		}
		for k, v := range want {
			gotVals := gotMap[k]
			if len(gotVals) != len(v) {
				t.Errorf("parseQuery(%q)[%q] = %v, want %v", raw, k, gotVals, v)
				continue
			}
			for i := range v {
				if gotVals[i] != v[i] {
					t.Errorf("parseQuery(%q)[%q][%d] = %q, want %q", raw, k, i, gotVals[i], v[i])
				}
			}
		}
		for k := range gotMap {
			if _, ok := want[k]; !ok {
				t.Errorf("parseQuery(%q) produced unexpected key %q", raw, k)
			}
		}
	}
}

// TestParseQueryDocumentedDeviations records where the hand-rolled parser
// intentionally differs from net/url:
//
//   - net/url.ParseQuery rejects ';' as a separator (and errors on it), while
//     the hand-rolled parser treats it as an ordinary character in the value.
//   - net/url returns a map (unordered, and it keeps empty-valued keys the
//     same way); parseQuery preserves wire order, which the binding does not
//     depend on but tests and error messages do.
func TestParseQueryDocumentedDeviations(t *testing.T) {
	if _, err := url.ParseQuery("a=1;b=2"); err == nil {
		t.Fatal("expected net/url.ParseQuery to reject ';' — deviation record is stale")
	}
	pairs, ok := parseQuery("a=1;b=2")
	if !ok {
		t.Fatal("hand-rolled parser should accept ';' as a literal character")
	}
	if len(pairs) != 1 || pairs[0].key != "a" || pairs[0].value != "1;b=2" {
		t.Fatalf("pairs = %+v, want a single a=1;b=2 entry", pairs)
	}

	pairs, ok = parseQuery("b=2&a=1&a=3")
	if !ok {
		t.Fatal("unexpected parse failure")
	}
	var keys []string
	for _, p := range pairs {
		keys = append(keys, p.key)
	}
	if got := keys; len(got) != 3 || got[0] != "b" || got[1] != "a" || got[2] != "a" {
		t.Fatalf("keys = %v, want wire order preserved", got)
	}
}

// TestEmailRuleDifferential pins the hand-rolled email check against net/mail
// (test-only; net/mail would add ~55 KB gzip to the shipped wasm).
//
// The soundness property asserted here: every address the rule accepts must
// also be accepted by net/mail. Rejections are checked against an explicit
// deviation table, because the rule is deliberately stricter for the API case.
func TestEmailRuleDifferential(t *testing.T) {
	accepted := []string{
		"ada@example.com",
		"ada.lovelace@example.co.uk",
		"a+b@example.com",
		"first.last@sub.domain.example",
		"user_name@example.com",
		"USER@EXAMPLE.COM",
		"x@y.io",
		"n1234567@example.travel",
		"a!#$%&'*+-/=?^_`{|}~@example.com",
	}
	for _, addr := range accepted {
		if !isEmail(addr) {
			t.Errorf("isEmail(%q) = false, want true", addr)
		}
		if _, err := mail.ParseAddress(addr); err != nil {
			t.Errorf("soundness violated: we accept %q but net/mail rejects it (%v)", addr, err)
		}
	}

	// Documented deviations: net/mail accepts these, the REST rule rejects
	// them (display forms, undotted domains, one-character TLDs, literals).
	deviations := []struct {
		addr   string
		reason string
	}{
		{"Ada <ada@example.com>", "display-name form, not an addr-spec"},
		{`"a b"@example.com`, "quoted local part"},
		{"ada@localhost", "undotted domain"},
		{"ada@example.c", "one-character TLD"},
		{"ada@[127.0.0.1]", "address literal"},
		{"ada@example..com", "empty label"},
		{"ada@-example.com", "label starting with a hyphen"},
		{"ada@example.com ", "trailing space"},
	}
	for _, d := range deviations {
		if _, err := mail.ParseAddress(d.addr); err != nil {
			t.Logf("note: net/mail no longer accepts %q; deviation table can be tightened", d.addr)
		}
		if isEmail(d.addr) {
			t.Errorf("isEmail(%q) = true, want false (%s)", d.addr, d.reason)
		}
	}

	rejected := []string{
		"", "ada", "@example.com", "ada@", "ada@@example.com",
		"a b@example.com", "ada@exa mple.com", ".ada@example.com",
		"ada.@example.com", "ada..b@example.com", "ada@example.com\n",
	}
	for _, addr := range rejected {
		if isEmail(addr) {
			t.Errorf("isEmail(%q) = true, want false", addr)
		}
		if _, err := mail.ParseAddress(addr); err == nil {
			// Fine — net/mail is more permissive; this just documents it.
			t.Logf("note: net/mail accepts %q, the rule rejects it", addr)
		}
	}
}

// TestParseQueryRoundTripIsDeterministic guards the ordering guarantee the
// envelope's details and error messages rely on.
func TestParseQueryRoundTripIsDeterministic(t *testing.T) {
	raw := "z=1&a=2&m=3&a=4"
	first, _ := parseQuery(raw)
	second, _ := parseQuery(raw)
	got := make([]string, 0, len(first))
	for _, p := range first {
		got = append(got, p.key+"="+p.value)
	}
	want := make([]string, 0, len(second))
	for _, p := range second {
		want = append(want, p.key+"="+p.value)
	}
	if len(got) != len(want) {
		t.Fatalf("lengths differ: %v vs %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("run %d differs: %v vs %v", i, got, want)
		}
	}
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
}
