package pdf

import (
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// ErrEncrypted is returned when the document cannot be decrypted with the
// supplied password.
var ErrEncrypted = errors.New("pdf: document is encrypted; wrong or missing password")

func inflateRaw(data []byte) ([]byte, error) {
	fr := flate.NewReader(bytes.NewReader(data))
	defer fr.Close()
	out, err := io.ReadAll(io.LimitReader(fr, MaxDecodedSize))
	if err != nil && len(out) > 0 {
		return out, nil
	}
	return out, err
}

type xrefKind uint8

const (
	xrefFree xrefKind = iota
	xrefOffset
	xrefCompressed
)

type xrefEntry struct {
	kind   xrefKind
	gen    uint16
	offset int64  // kind==offset
	stream uint32 // kind==compressed: object stream number
	index  int    // kind==compressed: index within stream
}

type objStm struct {
	data    []byte
	offsets []int
	nums    []uint32
}

// cacheShards spreads lock contention across goroutines.
const cacheShards = 64

type cacheShard struct {
	mu sync.RWMutex
	m  map[uint32]Object
}

// Reader provides random access to the objects of a PDF file.
// A Reader is safe for concurrent use.
type Reader struct {
	data    []byte
	xref    map[uint32]xrefEntry
	Trailer Dict
	Version string

	crypt *securityHandler

	cache    [cacheShards]cacheShard
	stmMu    sync.Mutex
	stmCache map[uint32]*objStm

	repairOnce sync.Once
	repaired   bool
}

// NewReader parses the file structure of data. The slice must not be
// modified while the Reader is in use.
func NewReader(data []byte, password string) (*Reader, error) {
	r := &Reader{data: data, xref: make(map[uint32]xrefEntry, 1024), stmCache: map[uint32]*objStm{}}
	for i := range r.cache {
		r.cache[i].m = make(map[uint32]Object)
	}
	r.Version = parseHeaderVersion(data)
	if err := r.loadXref(); err != nil || r.Trailer == nil || r.Trailer["Root"] == nil || !r.xrefLooksValid() {
		if rerr := r.repair(); rerr != nil {
			if err != nil {
				return nil, fmt.Errorf("pdf: %v (repair failed: %w)", err, rerr)
			}
			return nil, rerr
		}
	}
	if enc := r.Trailer["Encrypt"]; enc != nil {
		ed, ok := r.resolveNoCrypt(enc).(Dict)
		if !ok {
			return nil, errors.New("pdf: invalid /Encrypt dictionary")
		}
		var id0 []byte
		if ida, ok := r.Resolve(r.Trailer["ID"]).(Array); ok && len(ida) > 0 {
			if s, ok := r.Resolve(ida[0]).(String); ok {
				id0 = []byte(s)
			}
		}
		h, err := newSecurityHandler(ed, id0, password)
		if err != nil {
			return nil, err
		}
		r.crypt = h
		if ref, ok := enc.(Ref); ok {
			h.encryptRef = ref
		}
	}
	// Catalog version can override header (§7.2.2).
	if cat, ok := r.Resolve(r.Trailer["Root"]).(Dict); ok {
		if v, ok := cat["Version"].(Name); ok && string(v) > r.Version {
			r.Version = string(v)
		}
	}
	return r, nil
}

func parseHeaderVersion(data []byte) string {
	head := data[:min(len(data), 1024)]
	i := bytes.Index(head, []byte("%PDF-"))
	if i < 0 || i+8 > len(head) {
		return ""
	}
	return string(head[i+5 : i+8])
}

// Data exposes the underlying bytes.
func (r *Reader) Data() []byte { return r.data }

// Encrypted reports whether the file uses a security handler.
func (r *Reader) Encrypted() bool { return r.crypt != nil }

// NumObjects returns the size of the cross-reference table.
func (r *Reader) NumObjects() int { return len(r.xref) }

// ---------------------------------------------------------------------------
// Cross-reference loading

func (r *Reader) loadXref() error {
	off, err := findStartXref(r.data)
	if err != nil {
		return err
	}
	seen := map[int64]bool{}
	first := true
	for off >= 0 && !seen[off] {
		seen[off] = true
		if off >= int64(len(r.data)) {
			return fmt.Errorf("pdf: xref offset %d out of range", off)
		}
		trailer, err := r.readXrefSection(off)
		if err != nil {
			return err
		}
		if first {
			r.Trailer = trailer
			first = false
		} else {
			for k, v := range trailer {
				if _, ok := r.Trailer[k]; !ok && k != "Prev" && k != "XRefStm" {
					r.Trailer[k] = v
				}
			}
		}
		// Hybrid-reference files.
		if xs, ok := Int(trailer["XRefStm"]); ok && !seen[int64(xs)] {
			seen[int64(xs)] = true
			if _, err := r.readXrefSection(int64(xs)); err != nil {
				return err
			}
		}
		prev, ok := Int(trailer["Prev"])
		if !ok {
			break
		}
		off = int64(prev)
	}
	return nil
}

// xrefLooksValid cheaply checks that every in-use offset points at an
// object header for the right object number. Broken offsets are common in
// files edited by naive tools; when found we rebuild the table by scanning.
func (r *Reader) xrefLooksValid() bool {
	for num, e := range r.xref {
		if e.kind != xrefOffset {
			continue
		}
		if e.offset < 0 || e.offset >= int64(len(r.data)) {
			return false
		}
		p := NewParser(r.data, int(e.offset))
		n, err := strconv.ParseUint(string(p.ReadToken()), 10, 32)
		if err != nil || uint32(n) != num {
			return false
		}
	}
	return true
}

func findStartXref(data []byte) (int64, error) {
	tail := data[max(0, len(data)-4096):]
	i := bytes.LastIndex(tail, []byte("startxref"))
	if i < 0 {
		return 0, errors.New("pdf: startxref not found")
	}
	p := NewParser(tail, i+len("startxref"))
	tok := p.ReadToken()
	n, err := strconv.ParseInt(string(tok), 10, 64)
	if err != nil {
		return 0, errors.New("pdf: malformed startxref")
	}
	return n, nil
}

// setEntry records an entry unless a newer section already defined it.
func (r *Reader) setEntry(num uint32, e xrefEntry) {
	if _, ok := r.xref[num]; ok {
		return
	}
	r.xref[num] = e
}

func (r *Reader) readXrefSection(off int64) (Dict, error) {
	p := NewParser(r.data, int(off))
	p.SkipWS()
	if bytes.HasPrefix(r.data[p.Pos:], []byte("xref")) {
		p.Pos += 4
		return r.readXrefTable(p)
	}
	return r.readXrefStream(p)
}

func (r *Reader) readXrefTable(p *Parser) (Dict, error) {
	for {
		p.SkipWS()
		if bytes.HasPrefix(p.B[p.Pos:], []byte("trailer")) {
			p.Pos += len("trailer")
			o, err := p.ParseObject()
			if err != nil {
				return nil, err
			}
			d, ok := o.(Dict)
			if !ok {
				return nil, errors.New("pdf: malformed trailer")
			}
			return d, nil
		}
		start, err1 := strconv.Atoi(string(p.ReadToken()))
		count, err2 := strconv.Atoi(string(p.ReadToken()))
		if err1 != nil || err2 != nil || count < 0 {
			return nil, errors.New("pdf: malformed xref subsection")
		}
		for i := 0; i < count; i++ {
			offTok := p.ReadToken()
			genTok := p.ReadToken()
			typ := p.ReadToken()
			o, err1 := strconv.ParseInt(string(offTok), 10, 64)
			g, err2 := strconv.Atoi(string(genTok))
			if err1 != nil || err2 != nil || len(typ) != 1 {
				return nil, errors.New("pdf: malformed xref entry")
			}
			num := uint32(start + i)
			// Common off-by-one: first subsection starts at 1 but lists object 0.
			if i == 0 && start == 1 && o == 0 && g == 65535 && typ[0] == 'f' {
				start = 0
				num = 0
			}
			if typ[0] == 'n' {
				r.setEntry(num, xrefEntry{kind: xrefOffset, gen: uint16(g), offset: o})
			} else {
				r.setEntry(num, xrefEntry{kind: xrefFree})
			}
		}
	}
}

func (r *Reader) readXrefStream(p *Parser) (Dict, error) {
	obj, _, err := r.parseIndirectAt(p.Pos, nil)
	if err != nil {
		return nil, err
	}
	s, ok := obj.(*Stream)
	if !ok {
		return nil, errors.New("pdf: xref offset does not point to xref table or stream")
	}
	data, err := r.Decode(s)
	if err != nil {
		return nil, err
	}
	wArr, _ := s.Dict["W"].(Array)
	if len(wArr) < 3 {
		return nil, errors.New("pdf: xref stream missing /W")
	}
	var w [3]int
	for i := 0; i < 3; i++ {
		w[i], _ = Int(wArr[i])
		if w[i] < 0 || w[i] > 8 {
			return nil, errors.New("pdf: invalid /W")
		}
	}
	size, _ := Int(s.Dict["Size"])
	index := []int{0, size}
	if ia, ok := s.Dict["Index"].(Array); ok && len(ia) >= 2 {
		index = index[:0]
		for _, v := range ia {
			n, _ := Int(v)
			index = append(index, n)
		}
	}
	rowLen := w[0] + w[1] + w[2]
	if rowLen == 0 {
		return nil, errors.New("pdf: invalid /W")
	}
	field := func(b []byte, width int, def uint64) uint64 {
		if width == 0 {
			return def
		}
		var v uint64
		for _, c := range b[:width] {
			v = v<<8 | uint64(c)
		}
		return v
	}
	pos := 0
	for k := 0; k+1 < len(index); k += 2 {
		start, count := index[k], index[k+1]
		for i := 0; i < count && pos+rowLen <= len(data); i++ {
			row := data[pos : pos+rowLen]
			pos += rowLen
			t := field(row, w[0], 1)
			f2 := field(row[w[0]:], w[1], 0)
			f3 := field(row[w[0]+w[1]:], w[2], 0)
			num := uint32(start + i)
			switch t {
			case 0:
				r.setEntry(num, xrefEntry{kind: xrefFree})
			case 1:
				r.setEntry(num, xrefEntry{kind: xrefOffset, offset: int64(f2), gen: uint16(f3)})
			case 2:
				r.setEntry(num, xrefEntry{kind: xrefCompressed, stream: uint32(f2), index: int(f3)})
			}
		}
	}
	return s.Dict, nil
}

// repair rebuilds the xref by scanning the file for "n g obj" headers.
func (r *Reader) repair() error {
	r.repaired = true
	r.xref = make(map[uint32]xrefEntry, 1024)
	data := r.data
	var trailer Dict
	for i := 0; i < len(data); {
		j := bytes.Index(data[i:], []byte("obj"))
		if j < 0 {
			break
		}
		at := i + j
		i = at + 3
		if at+3 < len(data) && IsRegular(data[at+3]) {
			continue
		}
		// Walk back: "num gen obj"
		k := at - 1
		for k >= 0 && isWhite[data[k]] {
			k--
		}
		ge := k + 1
		for k >= 0 && data[k] >= '0' && data[k] <= '9' {
			k--
		}
		gs := k + 1
		if gs == ge {
			continue
		}
		for k >= 0 && isWhite[data[k]] {
			k--
		}
		ne := k + 1
		for k >= 0 && data[k] >= '0' && data[k] <= '9' {
			k--
		}
		ns := k + 1
		if ns == ne || (k >= 0 && IsRegular(data[k])) {
			continue
		}
		num, err1 := strconv.ParseUint(string(data[ns:ne]), 10, 32)
		gen, err2 := strconv.ParseUint(string(data[gs:ge]), 10, 16)
		if err1 != nil || err2 != nil {
			continue
		}
		// Later definitions win (incremental updates).
		r.xref[uint32(num)] = xrefEntry{kind: xrefOffset, offset: int64(ns), gen: uint16(gen)}
	}
	// Trailers: take the last one with /Root.
	for i := 0; i < len(data); {
		j := bytes.Index(data[i:], []byte("trailer"))
		if j < 0 {
			break
		}
		p := NewParser(data, i+j+7)
		i = i + j + 7
		if o, err := p.ParseObject(); err == nil {
			if d, ok := o.(Dict); ok && d["Root"] != nil {
				trailer = d
			}
		}
	}
	// Index object streams so compressed objects are reachable.
	for num, e := range r.xref {
		obj, _, err := r.parseIndirectAt(int(e.offset), nil)
		if err != nil {
			continue
		}
		s, ok := obj.(*Stream)
		if !ok {
			continue
		}
		switch s.Dict["Type"] {
		case Name("ObjStm"):
			r.indexObjStm(num, s)
		case Name("XRef"):
			if trailer == nil && s.Dict["Root"] != nil {
				trailer = s.Dict
			}
		}
	}
	if trailer == nil {
		trailer = Dict{}
	}
	if trailer["Root"] == nil {
		for num := range r.xref {
			if d, ok := r.Resolve(Ref{Num: num, Gen: r.xref[num].gen}).(Dict); ok && d["Type"] == Name("Catalog") {
				trailer["Root"] = Ref{Num: num, Gen: r.xref[num].gen}
				break
			}
		}
	}
	if trailer["Root"] == nil {
		return errors.New("pdf: no document catalog found")
	}
	r.Trailer = trailer
	r.clearCache()
	return nil
}

func (r *Reader) indexObjStm(num uint32, s *Stream) {
	data, err := r.Decode(s)
	if err != nil {
		return
	}
	n, _ := Int(s.Dict["N"])
	p := NewParser(data, 0)
	for i := 0; i < n; i++ {
		on, err := strconv.ParseUint(string(p.ReadToken()), 10, 32)
		if err != nil {
			return
		}
		p.ReadToken()
		if _, exists := r.xref[uint32(on)]; !exists {
			r.xref[uint32(on)] = xrefEntry{kind: xrefCompressed, stream: num, index: i}
		}
	}
}

func (r *Reader) clearCache() {
	for i := range r.cache {
		r.cache[i].mu.Lock()
		r.cache[i].m = make(map[uint32]Object)
		r.cache[i].mu.Unlock()
	}
	r.stmMu.Lock()
	r.stmCache = map[uint32]*objStm{}
	r.stmMu.Unlock()
}

// ---------------------------------------------------------------------------
// Object access

// Resolve follows indirect references (bounded) and returns the target.
func (r *Reader) Resolve(o Object) Object {
	for i := 0; i < 32; i++ {
		ref, ok := o.(Ref)
		if !ok {
			return o
		}
		v, err := r.Object(ref)
		if err != nil {
			return nil
		}
		o = v
	}
	return nil
}

func (r *Reader) resolveNoCrypt(o Object) Object {
	ref, ok := o.(Ref)
	if !ok {
		return o
	}
	e, ok := r.xref[ref.Num]
	if !ok || e.kind != xrefOffset {
		return r.Resolve(o)
	}
	obj, _, err := r.parseIndirectAt(int(e.offset), nil)
	if err != nil {
		return nil
	}
	return obj
}

// Object loads an indirect object (cached, concurrency-safe).
func (r *Reader) Object(ref Ref) (Object, error) {
	sh := &r.cache[ref.Num%cacheShards]
	sh.mu.RLock()
	if v, ok := sh.m[ref.Num]; ok {
		sh.mu.RUnlock()
		return v, nil
	}
	sh.mu.RUnlock()

	v, err := r.load(ref)
	if err != nil {
		return nil, err
	}
	sh.mu.Lock()
	if old, ok := sh.m[ref.Num]; ok { // lost a benign race
		v = old
	} else {
		sh.m[ref.Num] = v
	}
	sh.mu.Unlock()
	return v, nil
}

func (r *Reader) load(ref Ref) (Object, error) {
	e, ok := r.xref[ref.Num]
	if !ok {
		return nil, nil // undefined objects are null (§7.3.10)
	}
	switch e.kind {
	case xrefFree:
		return nil, nil
	case xrefCompressed:
		return r.loadFromObjStm(e.stream, e.index, ref.Num)
	}
	var dec func(String) String
	if r.crypt != nil && ref != r.crypt.encryptRef {
		dec = r.crypt.stringDecrypter(ref)
	}
	obj, hdr, err := r.parseIndirectAt(int(e.offset), dec)
	if err != nil || hdr.Num != ref.Num {
		return nil, fmt.Errorf("pdf: object %v not found at offset %d", ref, e.offset)
	}
	if s, ok := obj.(*Stream); ok {
		s.Ref = ref
		if r.crypt != nil && ref != r.crypt.encryptRef {
			s.Raw = r.crypt.decryptStream(ref, s)
		}
	}
	return obj, nil
}

func (r *Reader) loadFromObjStm(stmNum uint32, index int, num uint32) (Object, error) {
	r.stmMu.Lock()
	os := r.stmCache[stmNum]
	r.stmMu.Unlock()
	if os == nil {
		so, err := r.Object(Ref{Num: stmNum})
		if err != nil {
			return nil, err
		}
		s, ok := so.(*Stream)
		if !ok {
			return nil, fmt.Errorf("pdf: object stream %d missing", stmNum)
		}
		data, err := r.Decode(s)
		if err != nil {
			return nil, err
		}
		n, _ := Int(s.Dict["N"])
		first, _ := Int(s.Dict["First"])
		if n < 0 || n > len(data) {
			return nil, errors.New("pdf: invalid object stream /N")
		}
		os = &objStm{data: data, offsets: make([]int, n), nums: make([]uint32, n)}
		p := NewParser(data, 0)
		for i := 0; i < n; i++ {
			on, _ := strconv.ParseUint(string(p.ReadToken()), 10, 32)
			off, _ := strconv.Atoi(string(p.ReadToken()))
			os.nums[i] = uint32(on)
			os.offsets[i] = first + off
		}
		r.stmMu.Lock()
		r.stmCache[stmNum] = os
		r.stmMu.Unlock()
	}
	// Prefer index, but verify the object number (some writers are wrong).
	if index < 0 || index >= len(os.nums) || os.nums[index] != num {
		index = -1
		for i, n := range os.nums {
			if n == num {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, nil
		}
	}
	off := os.offsets[index]
	if off < 0 || off >= len(os.data) {
		return nil, errors.New("pdf: invalid object stream offset")
	}
	p := NewParser(os.data, off)
	return p.ParseObject()
}

// parseIndirectAt parses "num gen obj ... endobj" at off.
func (r *Reader) parseIndirectAt(off int, dec func(String) String) (Object, Ref, error) {
	if off < 0 || off >= len(r.data) {
		return nil, Ref{}, errors.New("pdf: offset out of range")
	}
	p := NewParser(r.data, off)
	numTok := p.ReadToken()
	genTok := p.ReadToken()
	num, err1 := strconv.ParseUint(string(numTok), 10, 32)
	gen, err2 := strconv.ParseUint(string(genTok), 10, 16)
	if err1 != nil || err2 != nil {
		return nil, Ref{}, fmt.Errorf("pdf: no object header at %d", off)
	}
	if err := p.ExpectKeyword("obj"); err != nil {
		return nil, Ref{}, err
	}
	ref := Ref{Num: uint32(num), Gen: uint16(gen)}
	p.Decrypt = dec
	obj, err := p.ParseObject()
	if err != nil {
		return nil, ref, err
	}
	d, ok := obj.(Dict)
	if !ok {
		return obj, ref, nil
	}
	p.SkipWS()
	if !bytes.HasPrefix(r.data[p.Pos:], []byte("stream")) {
		return obj, ref, nil
	}
	p.Pos += len("stream")
	if p.Pos < len(r.data) && r.data[p.Pos] == '\r' {
		p.Pos++
	}
	if p.Pos < len(r.data) && r.data[p.Pos] == '\n' {
		p.Pos++
	}
	start := p.Pos
	length := -1
	switch l := d["Length"].(type) {
	case Integer:
		length = int(l)
	case Ref:
		if l.Num != ref.Num {
			if v, ok := Int(r.resolveNoCrypt(l)); ok {
				length = v
			}
		}
	}
	end := start + length
	if length < 0 || end > len(r.data) || !endstreamFollows(r.data, end) {
		// Length is wrong or missing: search for endstream.
		k := bytes.Index(r.data[start:], []byte("endstream"))
		if k < 0 {
			end = len(r.data)
		} else {
			end = start + k
			for end > start && (r.data[end-1] == '\n' || r.data[end-1] == '\r') {
				end--
			}
		}
	}
	return &Stream{Dict: d, Raw: r.data[start:end:end], Ref: ref}, ref, nil
}

func endstreamFollows(data []byte, pos int) bool {
	for pos < len(data) && isWhite[data[pos]] {
		pos++
	}
	return bytes.HasPrefix(data[pos:], []byte("endstream"))
}

// ---------------------------------------------------------------------------
// Typed helpers

// Dict resolves o as a dictionary (streams yield their dictionary).
func (r *Reader) Dict(o Object) Dict {
	switch v := r.Resolve(o).(type) {
	case Dict:
		return v
	case *Stream:
		return v.Dict
	}
	return nil
}

// Array resolves o as an array.
func (r *Reader) Array(o Object) Array { a, _ := r.Resolve(o).(Array); return a }

// Name resolves o as a name.
func (r *Reader) Name(o Object) Name { n, _ := r.Resolve(o).(Name); return n }

// Stream resolves o as a stream.
func (r *Reader) Stream(o Object) *Stream { s, _ := r.Resolve(o).(*Stream); return s }

// Num resolves o as a number.
func (r *Reader) Num(o Object, def float64) float64 {
	if v, ok := Num(r.Resolve(o)); ok {
		return v
	}
	return def
}

// Int resolves o as an integer.
func (r *Reader) Int(o Object, def int) int {
	if v, ok := Int(r.Resolve(o)); ok {
		return v
	}
	return def
}

// Numbers resolves o as an array of numbers.
func (r *Reader) Numbers(o Object) []float64 {
	a := r.Array(o)
	out := make([]float64, 0, len(a))
	for _, e := range a {
		v, ok := Num(r.Resolve(e))
		if !ok {
			return nil
		}
		out = append(out, v)
	}
	return out
}

// Catalog returns the document catalog.
func (r *Reader) Catalog() Dict { return r.Dict(r.Trailer["Root"]) }

// Info returns the document information dictionary (may be nil).
func (r *Reader) Info() Dict { return r.Dict(r.Trailer["Info"]) }
