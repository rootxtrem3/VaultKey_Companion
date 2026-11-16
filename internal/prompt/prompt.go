// Package prompt provides hidden secret entry and destructive-action
// confirmation without third-party dependencies.
package prompt

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
)

// ErrNoTerminal means no interactive terminal is available.
var ErrNoTerminal = errors.New("no interactive terminal available")

const maxSecretLength = 4096

func readLimitedLine(reader *bufio.Reader, maximum int) (string, error) {
	var line []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		line = append(line, chunk...)

		if newline := bytes.IndexByte(line, '\n'); newline >= 0 {
			return limitedLineValue(line[:newline], maximum)
		}
		// No terminator yet. A pending CR is not content, so a CRLF split
		// across reads does not count against the limit.
		if len(bytes.TrimSuffix(line, []byte{'\r'})) > maximum {
			return "", errors.New("input line is too long")
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			if len(line) == 0 {
				return "", io.EOF
			}
			return limitedLineValue(line, maximum)
		}
		if err != nil {
			return "", err
		}
	}
}

// limitedLineValue enforces the content limit after removing a single trailing
// CR. A trailing LF is already excluded, so exactly maximum bytes is accepted.
func limitedLineValue(content []byte, maximum int) (string, error) {
	content = bytes.TrimSuffix(content, []byte{'\r'})
	if len(content) > maximum {
		return "", errors.New("input line is too long")
	}
	return string(content), nil
}

func isYes(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
