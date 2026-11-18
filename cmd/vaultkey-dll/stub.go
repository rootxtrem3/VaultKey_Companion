//go:build !windows || !cgo

// Package main is a placeholder so that `go build ./...` and `go test ./...`
// keep working on hosts without a Windows cgo toolchain. The real DLL bridge
// lives in main.go and is built with:
//
//	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc \
//	go build -buildmode=c-shared -o vaultkey.dll ./cmd/vaultkey-dll
package main

func main() {}
