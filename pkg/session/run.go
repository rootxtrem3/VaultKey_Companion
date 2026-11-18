package session

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/rootxtrem3/VaultKey_Companion/internal/apperr"
	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

// MaxRequestLine is the largest accepted request line, excluding the newline.
const MaxRequestLine = 64 * 1024

// Run owns stdio for a session. It emits the ready event, reads JSON Lines,
// writes responses and events, and exits on shutdown, EOF, or interrupt.
func Run(ctx context.Context, in io.Reader, out, errOut io.Writer, options Options) int {
	engine, err := New(ctx, options)
	if err != nil {
		info := apperr.Classify(err)
		_ = writeMessage(out, event{Type: "event", Event: "fatal", Error: &info})
		return info.ExitCode
	}
	defer engine.Close()

	var writeMu sync.Mutex
	emit := func(line []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writeLine(out, line)
	}
	engine.setEmitter(emit)

	ready, err := engine.readyResult()
	if err != nil {
		info := apperr.Classify(err)
		_ = engine.emitEvent("fatal", nil, err)
		return info.ExitCode
	}
	if err := engine.emitEvent("ready", ready, nil); err != nil {
		return vaultkey.ExitOK
	}

	type lineResult struct {
		line    []byte
		tooLong bool
		err     error
	}
	lines := make(chan lineResult)
	go func() {
		reader := bufio.NewReaderSize(in, MaxRequestLine+1)
		for {
			line, tooLong, readErr := readRequestLine(reader)
			select {
			case lines <- lineResult{line: line, tooLong: tooLong, err: readErr}:
			case <-ctx.Done():
				return
			}
			if readErr != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return vaultkey.ExitInterrupted
		case result := <-lines:
			if result.err != nil {
				if errors.Is(result.err, io.EOF) {
					return vaultkey.ExitOK
				}
				fmt.Fprintf(errOut, "session: read error: %v\n", result.err)
				return vaultkey.ExitFailure
			}
			if result.tooLong {
				tooLong := &LocalError{
					CodeName: "line_too_long",
					Message:  "request line exceeds 64 KiB",
				}
				if err := engine.respond(0, nil, tooLong); err != nil {
					return vaultkey.ExitOK
				}
				continue
			}
			line := bytes.TrimSpace(result.line)
			if len(line) == 0 {
				continue
			}
			if err := engine.Handle(ctx, line, emit); err != nil {
				// A failed stdout write means the UI is gone.
				return vaultkey.ExitOK
			}
			if engine.ShutdownRequested() {
				return vaultkey.ExitOK
			}
		}
	}
}

type readyResult struct {
	SessionProtocol int              `json:"session_protocol"`
	App             vaultkey.AppInfo `json:"app"`
	Port            string           `json:"port"`
	Device          vaultkey.Hello   `json:"device"`
}

func (e *Engine) readyResult() (any, error) {
	return deviceCall(e, context.Background(), func(device Device) (readyResult, error) {
		return readyResult{
			SessionProtocol: SessionProtocol,
			App:             CapabilitiesFor(e.options).App,
			Port:            device.PortName(),
			Device:          device.Hello(),
		}, nil
	})
}

func readRequestLine(reader *bufio.Reader) ([]byte, bool, error) {
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = reader.ReadSlice('\n')
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, false, err
		}
		return nil, true, nil
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			if len(line) > 0 {
				return line, false, nil
			}
			return nil, false, io.EOF
		}
		return nil, false, err
	}
	return line, false, nil
}

func writeLine(out io.Writer, line []byte) error {
	payload := append(append([]byte(nil), line...), '\n')
	if _, err := out.Write(payload); err != nil {
		return err
	}
	if flusher, ok := out.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

func writeMessage(out io.Writer, message event) error {
	line, err := marshalLine(message)
	if err != nil {
		return err
	}
	return writeLine(out, line)
}
