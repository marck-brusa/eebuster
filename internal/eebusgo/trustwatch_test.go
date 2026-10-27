package eebusgo

import "testing"

// A dial refused on the cached fingerprint is recognised by its SKI, and only that refusal: other
// dial failures stay plain dial errors.
func TestDialCertificateMismatch(t *testing.T) {
	const ski = "0123456789abcdef0123456789abcdef01234567"
	cases := []struct {
		line string
		want bool
	}{
		{"connection to " + ski + " at 192.0.2.10 failed:peer certificate rejected during TLS handshake: fingerprint mismatch: expected 0154, got 2B47", true},
		{"connection to " + ski + " at 192.0.2.10 failed:dial tcp 192.0.2.10:4712: connect: connection refused", false},
		{"connection to " + ski + " at 192.0.2.10 failed:SKI mismatch: expected " + ski + ", got 89ab", false},
	}
	for _, c := range cases {
		if got := dialCertificateMismatchRe.MatchString(c.line); got != c.want {
			t.Errorf("%q: got %v, want %v", c.line, got, c.want)
		}
		if m := dialFailureRe.FindStringSubmatch(c.line); m == nil || m[1] != ski {
			t.Errorf("%q: the dial failure names no SKI: %v", c.line, m)
		}
	}
}
