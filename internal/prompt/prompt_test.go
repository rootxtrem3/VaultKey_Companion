package prompt

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadLimitedLine(t *testing.T) {
	const limit = 5
	exact := strings.Repeat("a", limit)
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"exact limit with LF", exact + "\n", exact, false},
		{"exact limit with CRLF", exact + "\r\n", exact, false},
		{"exact limit without newline", exact, exact, false},
		{"over limit with LF", exact + "a\n", "", true},
		{"over limit with CRLF", exact + "a\r\n", "", true},
		{"over limit without newline", exact + "a", "", true},
	}
	for _, test := range cases {
		got, err := readLimitedLine(bufio.NewReader(strings.NewReader(test.input)), limit)
		if test.wantErr {
			if err == nil {
				t.Errorf("%s: expected an error, got %q", test.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", test.name, err)
			continue
		}
		if got != test.want {
			t.Errorf("%s: got %q, want %q", test.name, got, test.want)
		}
	}
}

func TestReadLimitedLineEmptyInput(t *testing.T) {
	_, err := readLimitedLine(bufio.NewReader(strings.NewReader("")), 5)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("want io.EOF, got %v", err)
	}
}
