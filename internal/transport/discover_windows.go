//go:build windows

package transport

import (
	"fmt"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

const (
	errorNoMoreItems syscall.Errno = 259
	regSZ                          = 1
)

var (
	advapi32           = syscall.NewLazyDLL("advapi32.dll")
	procRegEnumValueW  = advapi32.NewProc("RegEnumValueW")
	procRegCloseKey    = advapi32.NewProc("RegCloseKey")
	serialCommRegistry = `HARDWARE\DEVICEMAP\SERIALCOMM`
)

// Candidates enumerates COM ports from the Windows SERIALCOMM registry key.
// Ports still have to be probed with HELLO to identify a VaultKey.
func Candidates() ([]string, error) {
	var key syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_LOCAL_MACHINE,
		syscall.StringToUTF16Ptr(serialCommRegistry), 0, syscall.KEY_READ,
		&key); err != nil {
		return nil, fmt.Errorf("open %s: %w", serialCommRegistry, err)
	}
	defer procRegCloseKey.Call(uintptr(key))

	var values, maxName, maxValue uint32
	if err := syscall.RegQueryInfoKey(key, nil, nil, nil, nil, nil, nil,
		&values, &maxName, &maxValue, nil, nil); err != nil {
		return nil, fmt.Errorf("query %s: %w", serialCommRegistry, err)
	}

	ports := make([]string, 0, values)
	for index := uint32(0); index < values; index++ {
		name := make([]uint16, maxName+1)
		data := make([]byte, (maxValue+1)*2)
		nameLength := uint32(len(name))
		dataLength := uint32(len(data))
		var valueType uint32

		result, _, callErr := procRegEnumValueW.Call(
			uintptr(key), uintptr(index),
			uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&nameLength)),
			0, uintptr(unsafe.Pointer(&valueType)),
			uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&dataLength)),
		)
		if result == uintptr(errorNoMoreItems) {
			break
		}
		if result != 0 {
			return nil, fmt.Errorf("enumerate %s: %w", serialCommRegistry, callErr)
		}
		if valueType != regSZ || dataLength < 2 {
			continue
		}
		chars := unsafe.Slice((*uint16)(unsafe.Pointer(&data[0])), dataLength/2)
		port := strings.TrimSpace(syscall.UTF16ToString(chars))
		if port != "" {
			ports = append(ports, port)
		}
	}

	sort.Slice(ports, func(i, j int) bool {
		return strings.ToUpper(ports[i]) < strings.ToUpper(ports[j])
	})
	return ports, nil
}
