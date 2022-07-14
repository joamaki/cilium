// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

//go:build mock

package bpf

import (
	"fmt"
	"io"
	"unsafe"

	"golang.org/x/exp/slices"
	"golang.org/x/sys/unix"

	"github.com/cilium/cilium/pkg/lock"
)

type mapEntry struct {
	key   []byte
	value []byte
}

type mapMock struct {
	fd                 int
	mapType            MapType
	path               string
	keySize, valueSize uint32
	maxEntries         uint32
	flags              uint32
	innerID            uint32

	entries []mapEntry
}

// This struct must be in sync with union bpf_attr's anonymous struct used by
// BPF_MAP_*_ELEM commands
type bpfAttrMapOpElem struct {
	mapFd uint32
	pad0  [4]byte
	key   uint64
	value uint64 // union: value or next_key
	flags uint64
}

var (
	mu          lock.RWMutex
	nextFd      int = 10000 // NOTE: try not to overlap with real fds. Someone is calling close()
	openMaps        = make(map[string]*mapMock)
	mapFdToPath     = make(map[int]string)
)

func ResetMockMaps() {
	mu.Lock()
	defer mu.Unlock()
	openMaps = make(map[string]*mapMock)
	mapFdToPath = make(map[int]string)
	nextFd = 10000
}

func MockDumpMaps() {
	mu.RLock()
	mu.RUnlock()

	fmt.Printf("Mock maps:\n")
	for name, m := range openMaps {
		rmap := GetMap(m.path)
		if rmap != nil {
			fmt.Printf("\t%s: fd=%d, mapType=%s, numEntries=%d, valueType=%T\n", name, m.fd, m.mapType, len(m.entries), rmap.MapInfo.MapValue)

			hash := make(map[string][]string)
			if err := rmap.Dump(hash); err != nil {
				panic(err)
			}
			for k, v := range hash {
				fmt.Printf("\t    %q: %q\n", k, v)
			}
		} else {
			fmt.Printf("\t%s: fd=%d, mapType=%s, numEntries=%d, valueType=unknown\n", name, m.fd, m.mapType, len(m.entries))
		}
	}

}

func createMap(mapType MapType, keySize, valueSize, maxEntries, flags, innerID uint32, fullPath string) (int, error) {
	fmt.Printf(">>> createMap %q\n", fullPath)

	if _, ok := openMaps[fullPath]; ok {
		return 0, fmt.Errorf("map %q already exists", fullPath)
	}

	nextFd++
	openMaps[fullPath] = &mapMock{
		fd:         nextFd,
		mapType:    mapType,
		path:       fullPath,
		keySize:    keySize,
		valueSize:  valueSize,
		maxEntries: maxEntries,
		flags:      flags,
		innerID:    innerID,
	}
	mapFdToPath[nextFd] = fullPath
	return nextFd, nil
}

func CreateMap(mapType MapType, keySize, valueSize, maxEntries, flags, innerID uint32, fullPath string) (int, error) {
	mu.Lock()
	defer mu.Unlock()
	return createMap(mapType, keySize, valueSize, maxEntries, flags, innerID, fullPath)
}

func OpenOrCreateMap(path string, mapType MapType, keySize, valueSize, maxEntries, flags uint32, innerID uint32, pin bool) (int, bool, error) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Printf(">>> OpenOrCreateMap %q\n", path)
	if m, ok := openMaps[path]; ok {
		return m.fd, false, nil
	}
	fd, err := createMap(mapType, keySize, valueSize, maxEntries, flags, innerID, path)
	return fd, true, err
}

func ObjGet(pathname string) (int, error) {
	fmt.Printf(">>> ObjGet(%s)\n", pathname)
	mu.RLock()
	defer mu.RUnlock()

	if m, ok := openMaps[pathname]; ok {
		return m.fd, nil
	}
	return 0, fmt.Errorf("%q does not exist", pathname)
}

func GetFirstKey(fd int, nextKey unsafe.Pointer) error {
	mu.RLock()
	defer mu.RUnlock()
	m, ok := openMaps[mapFdToPath[fd]]
	if !ok {
		return fmt.Errorf("map with fd does not exist", fd)
	}
	if len(m.entries) > 0 {
		to := unsafe.Slice((*byte)(nextKey), m.keySize)
		copy(to, m.entries[0].key)
		return nil
	}
	return io.EOF
}

func LookupElementFromPointers(fd int, structPtr unsafe.Pointer, sizeOfStruct uintptr) error {
	mu.RLock()
	defer mu.RUnlock()

	uba := (*bpfAttrMapOpElem)(structPtr)

	m, ok := openMaps[mapFdToPath[fd]]
	if !ok {
		return fmt.Errorf("map with fd %d not found", fd)
	}

	key := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(uba.key))), m.keySize)

	idx := slices.IndexFunc(m.entries,
		func(e mapEntry) bool {
			return slices.Equal(key, e.key)
		})
	if idx < 0 {
		return fmt.Errorf("not found")
	}
	outValue := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(uba.value))), m.valueSize)
	copy(outValue, m.entries[idx].value)
	return nil
}
func GetNextKeyFromPointers(fd int, structPtr unsafe.Pointer, sizeOfStruct uintptr) error {
	uba := (*bpfAttrMapOpElem)(structPtr)
	return GetNextKey(fd, unsafe.Pointer(uintptr(uba.key)), unsafe.Pointer(uintptr(uba.value)))
}

func LookupElement(fd int, key, value unsafe.Pointer) error {
	mu.RLock()
	defer mu.RUnlock()

	m, ok := openMaps[mapFdToPath[fd]]
	if !ok {
		return fmt.Errorf("map with fd %d not found", fd)
	}
	keyBytes := unsafe.Slice((*byte)(key), m.keySize)

	idx := slices.IndexFunc(m.entries,
		func(e mapEntry) bool {
			return slices.Equal(keyBytes, e.key)
		})
	if idx < 0 {
		return fmt.Errorf("not found")
	}
	outValue := unsafe.Slice((*byte)(value), m.valueSize)
	copy(outValue, m.entries[idx].value)
	return nil
}

func deleteElement(fd int, key unsafe.Pointer) (uintptr, unix.Errno) {
	mu.Lock()
	defer mu.Unlock()

	m, ok := openMaps[mapFdToPath[fd]]
	if !ok {
		return 0, unix.ENOENT
	}
	keyBytes := unsafe.Slice((*byte)(key), m.keySize)

	idx := slices.IndexFunc(m.entries,
		func(e mapEntry) bool {
			return slices.Equal(keyBytes, e.key)
		})
	if idx < 0 {
		return 0, unix.ENOENT
	}
	m.entries = append(m.entries[:idx], m.entries[idx+1:]...)
	return 0, 0
}

func DeleteElement(fd int, key unsafe.Pointer) error {
	_, errno := deleteElement(fd, key)
	if errno != 0 {
		return fmt.Errorf("DeleteElement failed: %s", errno)
	}
	return nil
}

func GetNextKey(fd int, key, nextKey unsafe.Pointer) error {
	mu.RLock()
	defer mu.RUnlock()
	m, ok := openMaps[mapFdToPath[fd]]
	if !ok {
		return fmt.Errorf("map with fd does not exist", fd)
	}
	keyBytes := unsafe.Slice((*byte)(key), m.keySize)
	idx := slices.IndexFunc(m.entries,
		func(e mapEntry) bool {
			return slices.Equal(keyBytes, e.key)
		})
	if idx < 0 {
		return fmt.Errorf("not found")
	}
	if idx+1 >= len(m.entries) {
		return io.EOF
	}
	to := unsafe.Slice((*byte)(nextKey), m.keySize)
	copy(to, m.entries[idx+1].key)
	return nil
}

func UpdateElement(fd int, mapName string, key, value unsafe.Pointer, flags uint64) error {
	mu.Lock()
	defer mu.Unlock()

	fmt.Printf(">>> UpdateElement(%d, %q)\n", fd, mapName)

	m, ok := openMaps[mapFdToPath[fd]]
	if !ok {
		return fmt.Errorf("map with fd %d not found", fd)
	}
	keyBytes := unsafe.Slice((*byte)(key), m.keySize)
	valueBytes := unsafe.Slice((*byte)(value), m.valueSize)

	idx := slices.IndexFunc(m.entries,
		func(e mapEntry) bool {
			return slices.Equal(keyBytes, e.key)
		})
	if idx < 0 {
		m.entries = append(m.entries, mapEntry{keyBytes, valueBytes})
	} else {
		m.entries[idx].value = valueBytes
	}
	return nil
}

func objCheck(fd int, path string, mapType MapType, keySize, valueSize, maxEntries, flags uint32) bool {
	panic("objCheck")
}
func ObjClose(fd int) error {
	panic("ObjClose")
}
func GetMtime() (uint64, error) {
	panic("GetMtime")
}
func GetJtime() (uint64, error) {
	panic("GetJtime")
}
func MapFdFromID(id int) (int, error) {
	panic("MapFdFromID")
}
func ObjPin(fd int, pathname string) error {
	panic("ObjPin")
}
func TestDummyProg(progType ProgType, attachType uint32) error {
	return nil
}
