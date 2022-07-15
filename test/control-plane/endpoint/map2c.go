package endpoint

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strings"
	"unsafe"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/btf"

	"github.com/cilium/cilium/pkg/bpf"
)

func map2c(mapNameInBTF string, m *bpf.Map, outFile string) {
	w, err := os.OpenFile(outFile, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer w.Close()

	spec, err := ebpf.LoadCollectionSpec("/home/jussi/go/src/github.com/cilium/cilium/bpf/bpf_lxc.o")
	if err != nil {
		panic(err)
	}

	mapSpec, ok := spec.Maps[mapNameInBTF]
	if !ok {
		panic(err)
	}

	keyType := mapSpec.Key
	valueType := mapSpec.Value

	fmt.Fprintf(w, "struct { %s key; %s value; } values[] = {\n", typeName(keyType), typeName(valueType))

	m.DumpWithCallback(func(key bpf.MapKey, value bpf.MapValue) {
		keyS := unsafe.Slice((*byte)(key.GetKeyPtr()), m.KeySize)
		valueS := unsafe.Slice((*byte)(value.GetValuePtr()), m.ValueSize)
		fmt.Fprintf(w, "  {\n    .key = %s,\n", BtfBytesToCValue(keyType, keyS, 4))
		fmt.Fprintf(w, "    .value = %s\n  },\n", BtfBytesToCValue(valueType, valueS, 4))
	})
}

// copy-pasta from dump2c and Dylan's edb:
func typeName(typ btf.Type) string {
	switch typ := typ.(type) {
	case *btf.Struct:
		return "struct " + typ.Name
	case *btf.Union:
		return "union " + typ.Name
	case *btf.Typedef:
		return typ.Name
	default:
		panic(fmt.Sprintf("unhandled type %T", typ))
	}
}

func BtfBytesToCValue(t btf.Type, val []byte, depth int) string {
	var sb strings.Builder
	btfBytesToCValue(&sb, "", t, val, depth)
	return sb.String()
}

func btfBytesToCValue(sb *strings.Builder, fieldName string, t btf.Type, val []byte, depth int) []byte {
	switch t := t.(type) {
	case *btf.Array:
		fmt.Fprint(sb, "{")
		for i := 0; i < int(t.Nelems); i++ {
			val = btfBytesToCValue(sb, "", t.Type, val, depth)
			if i+1 < int(t.Nelems) {
				fmt.Fprint(sb, ", ")
			}
		}
		fmt.Fprint(sb, "}")

	case *btf.Const:
		return btfBytesToCValue(sb, fieldName, t.Type, val, 0)

	case *btf.Datasec:
		// Datasec isn't a C data type but a descriptor for a ELF section.
		return val

	case *btf.Enum:
		// TODO are enums always 32 bit?
		enumVal := int32(binary.LittleEndian.Uint32(val[:4]))
		for _, v := range t.Values {
			if v.Value == enumVal {
				fmt.Fprint(sb, v.Name)
				break
			}
		}
		return val[4:]

	case *btf.Float:
		switch t.Size {
		case 4:
			bits := binary.LittleEndian.Uint32(val[:4])
			fmt.Fprint(sb, math.Float32frombits(bits))
			return val[4:]
		case 8:
			bits := binary.LittleEndian.Uint64(val[:8])
			fmt.Fprint(sb, math.Float64frombits(bits))
			return val[8:]
		}

	case *btf.Func:
		// Can't print a function as value
		return val

	case *btf.FuncProto:
		// Can't print a func prototype on its own. print the btf.Func parent instread
		return val

	case *btf.Fwd:
		// Can't print a forward decleration as value
		return val

	case *btf.Int:
		//fmt.Fprintf(os.Stderr, "int, size: %d, len: %d\n", t.Size, len(val))
		if t.Encoding&btf.Bool > 0 {
			var boolVal bool
			for _, b := range val[:t.Size] {
				if b > 0 {
					boolVal = true
				}
			}

			fmt.Fprint(sb, boolVal)

		} else if t.Encoding&btf.Char > 0 {
			fmt.Fprint(sb, rune(val[0]))

		} else {
			var i uint64
			switch t.Size {
			case 1:
				i = uint64(val[0])
			case 2:
				i = uint64(binary.LittleEndian.Uint16(val[:2]))
			case 4:
				i = uint64(binary.LittleEndian.Uint32(val[:4]))
			case 8:
				i = uint64(binary.LittleEndian.Uint64(val[:8]))
			}

			if t.Size == 2 && strings.Contains(fieldName, "port") {
				fmt.Fprintf(sb, "%d", i)
			} else if t.Size == 4 && strings.Contains(fieldName, "addr") {
				fmt.Fprintf(sb, "IP4(%d, %d, %d, %d)",
					val[0],
					val[1],
					val[2],
					val[3])
			} else if t.Encoding&btf.Signed == 0 {
				fmt.Fprintf(sb, "0x%x", i)
			} else {
				fmt.Fprintf(sb, "0x%x", int64(i))
			}
		}

		return val[t.Size:]

	case *btf.Pointer:
		return btfBytesToCValue(sb, fieldName, t.Target, val, 0)

	case *btf.Restrict:
		return btfBytesToCValue(sb, "", t.Type, val, 0)

	case *btf.Struct:
		fmt.Fprint(sb, "{\n")

		var newVal []byte
		for _, m := range t.Members {
			if m.Name != "" {
				fmt.Fprint(sb, strings.Repeat(" ", depth+2))
				fmt.Fprint(sb, ".", m.Name, " = ")
			} else {
				fmt.Fprint(sb, strings.Repeat(" ", depth+2))
			}

			off := m.Offset.Bytes()

			newVal = btfBytesToCValue(sb, m.Name, m.Type, val[off:], depth)
			fmt.Fprint(sb, ",\n")
		}

		fmt.Fprint(sb, strings.Repeat(" ", depth), "}")
		return newVal

	case *btf.Typedef:
		return btfBytesToCValue(sb, fieldName, t.Type, val, 0)

	case *btf.Union:
		if fieldName != "" {
			fmt.Fprint(sb, "{\n")
		}
		first := true
		var newVal []byte
		for i, m := range t.Members {
			if !first {
				fmt.Fprint(sb, strings.Repeat(" ", depth+2))
				fmt.Fprint(sb, "// ")
			}

			innerFieldName := fieldName
			if m.Name != "" {
				fmt.Fprint(sb, ".", m.Name, " = ")
				innerFieldName = m.Name
			}

			if first {
				off := m.Offset.Bytes()

				btfBytesToCValue(sb, innerFieldName, m.Type, val[off:], depth+2)

				if i != len(t.Members)-1 {
					fmt.Fprint(sb, ",\n")
				}
			} else {
				fmt.Fprint(sb, " <union alternative omitted>")
			}

			first = false
		}
		if fieldName != "" {
			fmt.Fprint(sb, "\n", strings.Repeat(" ", depth), "}")
		}
		return newVal

	case *btf.Var:
		fmt.Fprint(sb, t.Name, " = ")
		return btfBytesToCValue(sb, "", t.Type, val, 0)

	case *btf.Void:
		fmt.Fprint(sb, "void")
		return val

	case *btf.Volatile:
		return btfBytesToCValue(sb, "", t.Type, val, 0)
	}

	return val
}
