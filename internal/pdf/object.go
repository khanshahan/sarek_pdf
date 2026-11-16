// Package pdf implements a self-contained, allocation-conscious PDF object
// layer: lexing, parsing, cross-reference resolution (classic tables, xref
// streams, object streams, hybrid files), stream filters and the standard
// security handler. It has no third-party dependencies.
//
// All types returned by a Reader are immutable after construction, so a
// Reader may be used concurrently from many goroutines.
package pdf

import (
	"fmt"
	"strconv"
)

// Object is any PDF object. The dynamic type is one of:
// nil (null), Boolean, Integer, Real, String, Name, Array, Dict, *Stream,
// Ref, or Keyword (content streams only).
type Object interface{}

// Boolean is a PDF boolean.
type Boolean bool

// Integer is a PDF integer.
type Integer int64

// Real is a PDF real number.
type Real float64

// String is a PDF string (raw bytes; already decrypted).
type String string

// Name is a PDF name without the leading slash.
type Name string

// Keyword is a bare token such as an operator in a content stream.
type Keyword string

// Array is a PDF array.
type Array []Object

// Dict is a PDF dictionary.
type Dict map[Name]Object

// Ref is an indirect reference.
type Ref struct {
	Num uint32
	Gen uint16
}

func (r Ref) String() string { return fmt.Sprintf("%d %d R", r.Num, r.Gen) }

// Stream is a PDF stream. Raw holds the undecoded (but decrypted) bytes.
type Stream struct {
	Dict Dict
	Raw  []byte
	Ref  Ref
}

// Num converts a numeric object to float64.
func Num(o Object) (float64, bool) {
	switch v := o.(type) {
	case Integer:
		return float64(v), true
	case Real:
		return float64(v), true
	}
	return 0, false
}

// Int converts a numeric object to int.
func Int(o Object) (int, bool) {
	switch v := o.(type) {
	case Integer:
		return int(v), true
	case Real:
		return int(v), true
	}
	return 0, false
}

func (o Name) String() string { return "/" + string(o) }

// DebugString renders an object for diagnostics.
func DebugString(o Object) string {
	switch v := o.(type) {
	case nil:
		return "null"
	case Boolean:
		return strconv.FormatBool(bool(v))
	case Integer:
		return strconv.FormatInt(int64(v), 10)
	case Real:
		return strconv.FormatFloat(float64(v), 'f', -1, 64)
	case String:
		return strconv.Quote(string(v))
	case Name:
		return v.String()
	case Keyword:
		return string(v)
	case Ref:
		return v.String()
	case Array:
		s := "["
		for i, e := range v {
			if i > 0 {
				s += " "
			}
			s += DebugString(e)
		}
		return s + "]"
	case Dict:
		s := "<<"
		for k, e := range v {
			s += k.String() + " " + DebugString(e) + " "
		}
		return s + ">>"
	case *Stream:
		return DebugString(v.Dict) + " stream"
	}
	return fmt.Sprintf("%v", o)
}
