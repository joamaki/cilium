package tables

import (
	"fmt"
	"net"
	"reflect"
)

// IPFieldIndex is to index an a net.IP field.
type IPFieldIndex struct {
	Field string
}

func (i *IPFieldIndex) FromObject(obj interface{}) (bool, []byte, error) {
	v := reflect.ValueOf(obj)
	v = reflect.Indirect(v) // Dereference the pointer if any
	fv := v.FieldByName(i.Field)
	if !fv.IsValid() {
		return false, nil,
			fmt.Errorf("field '%s' for %#v is invalid", i.Field, obj)
	}
	val, ok := fv.Interface().(net.IP)
	if !ok {
		return false, nil, fmt.Errorf("field is of type %T; want a net.IP", val)
	}
	return true, val, nil
}

func (i *IPFieldIndex) FromArgs(args ...interface{}) ([]byte, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("must provide only a single argument")
	}

	v := reflect.ValueOf(args[0])
	if !v.IsValid() {
		return nil, fmt.Errorf("%#v is invalid", args[0])
	}

	val, ok := v.Interface().(net.IP)
	if !ok {
		return nil, fmt.Errorf("field is of type %T; want a net.IP", val)
	}
	return val, nil
}

// IPNetFieldIndex is to index an a net.IPNet field.
// Constructs an index key "<IP bytes>\n<mask bytes>".
type IPNetFieldIndex struct {
	Field string
}

func (i *IPNetFieldIndex) FromObject(obj interface{}) (bool, []byte, error) {
	v := reflect.ValueOf(obj)
	v = reflect.Indirect(v) // Dereference the pointer if any
	fv := v.FieldByName(i.Field)
	if !fv.IsValid() {
		return false, nil,
			fmt.Errorf("field '%s' for %#v is invalid", i.Field, obj)
	}
	if fv.IsNil() {
		return true, []byte{}, nil
	}
	fv = reflect.Indirect(fv) // Dereference the pointer if any
	val, ok := fv.Interface().(net.IPNet)
	if !ok {
		return false, nil, fmt.Errorf("field is of type %s; want a net.IPNet", fv.Type())
	}
	out := make([]byte, 0, len(val.IP)+1+len(val.Mask))
	out = append(out, val.IP...)
	out = append(out, byte('\n'))
	out = append(out, val.Mask...)
	return true, out, nil
}

func (i *IPNetFieldIndex) FromArgs(args ...interface{}) ([]byte, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("must provide only a single argument")
	}
	v := reflect.ValueOf(args[0])
	if !v.IsValid() || v.IsNil() {
		return []byte{}, nil
	}
	val, ok := v.Interface().(net.IPNet)
	if !ok {
		return nil, fmt.Errorf("field is of type %T; want a net.IPNet", val)
	}
	out := make([]byte, 0, len(val.IP)+1+len(val.Mask))
	out = append(out, val.IP...)
	out = append(out, byte('\n'))
	out = append(out, val.Mask...)
	return out, nil
}
