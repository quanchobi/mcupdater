package updater

import (
	"context"
	"strings"
	"testing"
	"testing/iotest"
)

func TestCurseForgeFingerprintProtocol(t *testing.T) {
	// Vectors use Austin Appleby's MurmurHash2 reference, little-endian with
	// seed 1, and CurseForge's four-byte whitespace filter. The remainder
	// lengths distinguish byte order, word boundaries, and tail handling.
	cases := []struct {
		name  string
		input string
		want  uint32
	}{
		{"empty", "", 1540447798},
		{"one-byte-tail", "a", 626045324},
		{"two-byte-tail", "ab", 1692487918},
		{"three-byte-tail", "abc", 1621425345},
		{"complete-word", "abcd", 3376380438},
		{"word-and-filtered-tail", "h\te l\nl\ro", 2788266382},
		{"binary-not-whitespace", "\x00\x0b\x0c\xff", 169082839},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, length, err := modIdentityHash(context.Background(), strings.NewReader(test.input))
			if err != nil {
				t.Fatal(err)
			}
			// Splitting every input byte also exercises a word crossing reads.
			got, err := curseForgeFingerprint(context.Background(), iotest.OneByteReader(strings.NewReader(test.input)), length)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("fingerprint = %d, want %d", got, test.want)
			}
		})
	}
}

func TestCurseForgeFingerprintRejectsChangedLength(t *testing.T) {
	_, err := curseForgeFingerprint(context.Background(), strings.NewReader("changed"), 6)
	if err == nil {
		t.Fatal("accepted a JAR that changed between length and fingerprint passes")
	}
}
