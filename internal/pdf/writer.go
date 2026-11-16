package pdf

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
)

// AppendObject serialises o in PDF syntax. Streams are written with an
// updated /Length.
func AppendObject(b []byte, o Object) []byte {
	switch v := o.(type) {
	case nil:
		return append(b, "null"...)
	case Boolean:
		return strconv.AppendBool(b, bool(v))
	case Integer:
		return strconv.AppendInt(b, int64(v), 10)
	case Real:
		return appendReal(b, float64(v))
	case float64:
		return appendReal(b, v)
	case int:
		return strconv.AppendInt(b, int64(v), 10)
	case String:
		return appendString(b, v)
	case Name:
		return appendName(b, v)
	case Keyword:
		return append(b, v...)
	case Ref:
		b = strconv.AppendUint(b, uint64(v.Num), 10)
		b = append(b, ' ')
		b = strconv.AppendUint(b, uint64(v.Gen), 10)
		return append(b, " R"...)
	case Array:
		b = append(b, '[')
		for i, e := range v {
			if i > 0 {
				b = append(b, ' ')
			}
			b = AppendObject(b, e)
		}
		return append(b, ']')
	case Dict:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, string(k))
		}
		sort.Strings(keys) // deterministic output
		b = append(b, "<<"...)
		for _, k := range keys {
			b = appendName(b, Name(k))
			b = append(b, ' ')
			b = AppendObject(b, v[Name(k)])
		}
		return append(b, ">>"...)
	case *Stream:
		d := Dict{}
		for k, e := range v.Dict {
			d[k] = e
		}
		d["Length"] = Integer(len(v.Raw))
		b = AppendObject(b, d)
		b = append(b, "\nstream\n"...)
		b = append(b, v.Raw...)
		return append(b, "\nendstream"...)
	}
	return append(b, "null"...)
}

func appendReal(b []byte, f float64) []byte {
	if f == float64(int64(f)) && f < 1e15 && f > -1e15 {
		return strconv.AppendInt(b, int64(f), 10)
	}
	return strconv.AppendFloat(b, f, 'f', 4, 64)
}

func appendString(b []byte, s String) []byte {
	b = append(b, '(')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '(', ')', '\\':
			b = append(b, '\\', c)
		case '\r':
			b = append(b, '\\', 'r')
		case '\n':
			b = append(b, '\\', 'n')
		default:
			b = append(b, c)
		}
	}
	return append(b, ')')
}

func appendName(b []byte, n Name) []byte {
	const hex = "0123456789ABCDEF"
	b = append(b, '/')
	for i := 0; i < len(n); i++ {
		c := n[i]
		if c < 0x21 || c > 0x7E || c == '#' || isDelim[c] {
			b = append(b, '#', hex[c>>4], hex[c&15])
			continue
		}
		b = append(b, c)
	}
	return b
}

// TextStringObject encodes s as a PDF text string (PDFDocEncoding for
// ASCII, UTF-16BE with BOM otherwise).
func TextStringObject(s string) String {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return String(s)
	}
	out := []byte{0xFE, 0xFF}
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			hi, lo := 0xD800+(r>>10), 0xDC00+(r&0x3FF)
			out = append(out, byte(hi>>8), byte(hi), byte(lo>>8), byte(lo))
			continue
		}
		out = append(out, byte(r>>8), byte(r))
	}
	return String(out)
}

// FileWriter writes a PDF file body, tracking object offsets.
type FileWriter struct {
	w       *bufio.Writer
	off     int64
	offsets map[uint32]int64
	buf     []byte
	err     error
}

// NewFileWriter wraps w. base is the number of bytes already present
// before w's position (for incremental updates).
func NewFileWriter(w io.Writer, base int64) *FileWriter {
	return &FileWriter{w: bufio.NewWriterSize(w, 1<<16), off: base, offsets: map[uint32]int64{}}
}

// Raw writes raw bytes.
func (fw *FileWriter) Raw(b []byte) {
	if fw.err != nil {
		return
	}
	n, err := fw.w.Write(b)
	fw.off += int64(n)
	fw.err = err
}

// Offset returns the current byte offset.
func (fw *FileWriter) Offset() int64 { return fw.off }

// WriteObject writes "num gen obj ... endobj".
func (fw *FileWriter) WriteObject(ref Ref, o Object) {
	fw.offsets[ref.Num] = fw.off
	fw.buf = fw.buf[:0]
	fw.buf = strconv.AppendUint(fw.buf, uint64(ref.Num), 10)
	fw.buf = append(fw.buf, ' ')
	fw.buf = strconv.AppendUint(fw.buf, uint64(ref.Gen), 10)
	fw.buf = append(fw.buf, " obj\n"...)
	fw.buf = AppendObject(fw.buf, o)
	fw.buf = append(fw.buf, "\nendobj\n"...)
	fw.Raw(fw.buf)
}

// WriteXrefTable writes a classic xref table covering the written
// objects, the trailer and startxref. trailer must contain /Size.
func (fw *FileWriter) WriteXrefTable(trailer Dict, includeFree0 bool) {
	start := fw.off
	nums := make([]int, 0, len(fw.offsets))
	for n := range fw.offsets {
		nums = append(nums, int(n))
	}
	sort.Ints(nums)
	fw.Raw([]byte("xref\n"))
	if includeFree0 {
		fw.Raw([]byte("0 1\n0000000000 65535 f\r\n"))
	}
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		fw.Raw([]byte(fmt.Sprintf("%d %d\n", nums[i], j-i+1)))
		for k := i; k <= j; k++ {
			fw.Raw([]byte(fmt.Sprintf("%010d 00000 n\r\n", fw.offsets[uint32(nums[k])])))
		}
		i = j + 1
	}
	fw.Raw([]byte("trailer\n"))
	fw.Raw(AppendObject(nil, trailer))
	fw.Raw([]byte(fmt.Sprintf("\nstartxref\n%d\n%%%%EOF\n", start)))
}

// WriteXrefStream writes a cross-reference stream as object num.
func (fw *FileWriter) WriteXrefStream(num uint32, trailer Dict) {
	fw.offsets[num] = fw.off
	nums := make([]int, 0, len(fw.offsets))
	for n := range fw.offsets {
		nums = append(nums, int(n))
	}
	sort.Ints(nums)
	var index Array
	var data []byte
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		index = append(index, Integer(nums[i]), Integer(j-i+1))
		for k := i; k <= j; k++ {
			o := fw.offsets[uint32(nums[k])]
			data = append(data, 1, byte(o>>32), byte(o>>24), byte(o>>16), byte(o>>8), byte(o), 0)
		}
		i = j + 1
	}
	d := Dict{}
	for k, v := range trailer {
		d[k] = v
	}
	d["Type"] = Name("XRef")
	d["W"] = Array{Integer(1), Integer(5), Integer(1)}
	d["Index"] = index
	start := fw.off
	fw.WriteObject(Ref{Num: num}, &Stream{Dict: d, Raw: data})
	fw.Raw([]byte(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", start)))
}

// Flush flushes buffered output.
func (fw *FileWriter) Flush() error {
	if fw.err != nil {
		return fw.err
	}
	return fw.w.Flush()
}

// UsesXrefStream reports whether the newest xref section is a stream.
func (r *Reader) UsesXrefStream() bool { return r.Trailer["Type"] == Name("XRef") }

// StartXref returns the offset of the newest xref section.
func (r *Reader) StartXref() int64 {
	off, _ := findStartXref(r.data)
	return off
}

// MaxObjectNumber returns the largest object number in use plus one.
func (r *Reader) MaxObjectNumber() uint32 {
	var m uint32
	for n := range r.xref {
		if n >= m {
			m = n + 1
		}
	}
	if s, ok := Int(r.Trailer["Size"]); ok && uint32(s) > m {
		m = uint32(s)
	}
	return m
}

// Repaired reports whether the cross-reference table had to be rebuilt.
func (r *Reader) Repaired() bool { return r.repaired }
