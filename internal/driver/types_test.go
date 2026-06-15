package driver

import "testing"

func TestValidTLSFingerprint(t *testing.T) {
	valid := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cases := []struct {
		name        string
		fingerprint string
		want        bool
	}{
		{name: "valid", fingerprint: valid, want: true},
		{name: "empty", fingerprint: "", want: false},
		{name: "too short", fingerprint: "sha256:aabbcc", want: false},
		{name: "wrong prefix", fingerprint: "sha1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", want: false},
		{name: "uppercase", fingerprint: "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", want: false},
		{name: "non hex", fingerprint: "sha256:gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidTLSFingerprint(tc.fingerprint); got != tc.want {
				t.Errorf("ValidTLSFingerprint(%q) = %v, want %v", tc.fingerprint, got, tc.want)
			}
		})
	}
}
