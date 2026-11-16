package font

import (
	"encoding/binary"
	"sync"

	"github.com/khanshahan/sarek_pdf/internal/pdf"
)

// sfntTable returns the named table of an sfnt font, or nil.
func sfntTable(data []byte, tag string) []byte {
	be := binary.BigEndian
	if len(data) < 12 {
		return nil
	}
	n := int(be.Uint16(data[4:]))
	for i := 0; i < n; i++ {
		rec := 12 + 16*i
		if rec+16 > len(data) {
			return nil
		}
		if string(data[rec:rec+4]) == tag {
			off, ln := int(be.Uint32(data[rec+8:])), int(be.Uint32(data[rec+12:]))
			if off < 0 || ln < 0 || off+ln > len(data) {
				return nil
			}
			return data[off : off+ln]
		}
	}
	return nil
}

// coverageGlyphs expands an OpenType Coverage table into glyph IDs in
// coverage-index order.
func coverageGlyphs(t []byte) []uint16 {
	be := binary.BigEndian
	if len(t) < 4 {
		return nil
	}
	var out []uint16
	switch be.Uint16(t) {
	case 1:
		n := int(be.Uint16(t[2:]))
		for i := 0; i < n && 4+2*i+2 <= len(t); i++ {
			out = append(out, be.Uint16(t[4+2*i:]))
		}
	case 2:
		n := int(be.Uint16(t[2:]))
		for i := 0; i < n && 4+6*i+6 <= len(t); i++ {
			r := t[4+6*i:]
			start, end, idx := be.Uint16(r), be.Uint16(r[2:]), int(be.Uint16(r[4:]))
			for g := int(start); g <= int(end) && g-int(start) < 0x10000; g++ {
				k := idx + g - int(start)
				for len(out) <= k {
					out = append(out, 0)
				}
				out[k] = uint16(g)
			}
		}
	}
	return out
}

// gsubLigatures reads ligature substitutions (lookup type 4, also inside
// extension lookups of type 7) and returns ligature glyph → component
// glyphs.
func gsubLigatures(gsub []byte) map[uint16][]uint16 {
	be := binary.BigEndian
	if len(gsub) < 10 {
		return nil
	}
	ll := int(be.Uint16(gsub[8:]))
	if ll <= 0 || ll+2 > len(gsub) {
		return nil
	}
	lookups := gsub[ll:]
	out := map[uint16][]uint16{}
	var subtable func(st []byte, typ int)
	subtable = func(st []byte, typ int) {
		if len(st) < 6 {
			return
		}
		switch typ {
		case 7: // extension
			if be.Uint16(st) != 1 || len(st) < 8 {
				return
			}
			et, off := int(be.Uint16(st[2:])), int(be.Uint32(st[4:]))
			if et != 7 && off > 0 && off < len(st) {
				subtable(st[off:], et)
			}
		case 4: // ligature substitution, format 1
			cov := coverageGlyphs(st[min(int(be.Uint16(st[2:])), len(st)):])
			nsets := int(be.Uint16(st[4:]))
			for i := 0; i < nsets && i < len(cov) && 6+2*i+2 <= len(st); i++ {
				so := int(be.Uint16(st[6+2*i:]))
				if so+2 > len(st) {
					continue
				}
				set := st[so:]
				nl := int(be.Uint16(set))
				for j := 0; j < nl && 2+2*j+2 <= len(set); j++ {
					lo := int(be.Uint16(set[2+2*j:]))
					if lo+4 > len(set) {
						continue
					}
					lig := set[lo:]
					g, nc := be.Uint16(lig), int(be.Uint16(lig[2:]))
					if nc < 1 || 4+2*(nc-1) > len(lig) {
						continue
					}
					comps := []uint16{cov[i]}
					for k := 0; k < nc-1; k++ {
						comps = append(comps, be.Uint16(lig[4+2*k:]))
					}
					if _, seen := out[g]; !seen {
						out[g] = comps
					}
				}
			}
		}
	}
	n := int(be.Uint16(lookups))
	for i := 0; i < n && 2+2*i+2 <= len(lookups); i++ {
		lo := int(be.Uint16(lookups[2+2*i:]))
		if lo+6 > len(lookups) {
			continue
		}
		lk := lookups[lo:]
		typ, ns := int(be.Uint16(lk)), int(be.Uint16(lk[4:]))
		if typ != 4 && typ != 7 {
			continue
		}
		for k := 0; k < ns && 6+2*k+2 <= len(lk); k++ {
			so := int(be.Uint16(lk[6+2*k:]))
			if so < len(lk) {
				subtable(lk[so:], typ)
			}
		}
	}
	return out
}

// glyphTextFromSFNT maps glyph IDs to text using the 'cmap' table and,
// for glyphs without a cmap entry (ligatures such as "ti", "ffi"), the
// GSUB ligature components. All information comes from the embedded font.
func glyphTextFromSFNT(data []byte) map[uint16]string {
	uni := glyphUnicodeFromSFNT(data)
	if len(uni) == 0 {
		return nil
	}
	out := make(map[uint16]string, len(uni))
	for g, r := range uni {
		out[g] = string(r)
	}
	ligs := gsubLigatures(sfntTable(data, "GSUB"))
	var expand func(g uint16, depth int) (string, bool)
	expand = func(g uint16, depth int) (string, bool) {
		if t, ok := out[g]; ok {
			return t, true
		}
		comps, ok := ligs[g]
		if !ok || depth > 4 {
			return "", false
		}
		var sb []byte
		for _, c := range comps {
			t, ok := expand(c, depth+1)
			if !ok {
				return "", false
			}
			sb = append(sb, t...)
		}
		return string(sb), true
	}
	for g := range ligs {
		if _, ok := out[g]; !ok {
			if t, ok := expand(g, 0); ok {
				out[g] = t
			}
		}
	}
	return out
}

// glyphUnicodeFromSFNT parses the 'cmap' table of an embedded TrueType or
// OpenType font and returns the reverse mapping glyph ID → Unicode. When
// several code points map to one glyph the lowest non-private-use one is
// kept. This uses only data inside the PDF, so it is exact, not a guess.
func glyphUnicodeFromSFNT(data []byte) map[uint16]rune {
	be := binary.BigEndian
	if len(data) < 12 {
		return nil
	}
	numTables := int(be.Uint16(data[4:]))
	var cmap []byte
	for i := 0; i < numTables; i++ {
		rec := 12 + 16*i
		if rec+16 > len(data) {
			return nil
		}
		if string(data[rec:rec+4]) == "cmap" {
			off, ln := int(be.Uint32(data[rec+8:])), int(be.Uint32(data[rec+12:]))
			if off < 0 || ln < 0 || off+ln > len(data) {
				return nil
			}
			cmap = data[off : off+ln]
		}
	}
	if len(cmap) < 4 {
		return nil
	}
	n := int(be.Uint16(cmap[2:]))
	best, bestRank := -1, 0
	for i := 0; i < n; i++ {
		rec := 4 + 8*i
		if rec+8 > len(cmap) {
			break
		}
		pid, eid := be.Uint16(cmap[rec:]), be.Uint16(cmap[rec+2:])
		off := int(be.Uint32(cmap[rec+4:]))
		rank := 0
		switch {
		case pid == 3 && eid == 10:
			rank = 4
		case pid == 0 && (eid == 4 || eid == 6):
			rank = 3
		case pid == 3 && eid == 1:
			rank = 2
		case pid == 0:
			rank = 1
		}
		if rank > bestRank && off < len(cmap) {
			best, bestRank = off, rank
		}
	}
	if best < 0 || best+4 > len(cmap) {
		return nil
	}
	out := map[uint16]rune{}
	add := func(cp rune, gid uint16) {
		if gid == 0 || cp == 0 {
			return
		}
		pua := cp >= 0xE000 && cp <= 0xF8FF
		if old, ok := out[gid]; ok {
			oldPUA := old >= 0xE000 && old <= 0xF8FF
			if (oldPUA && !pua) || (oldPUA == pua && cp < old) {
				out[gid] = cp
			}
			return
		}
		out[gid] = cp
	}
	t := cmap[best:]
	switch be.Uint16(t) {
	case 4:
		if len(t) < 14 {
			return nil
		}
		segX2 := int(be.Uint16(t[6:]))
		need := 16 + 4*segX2
		if len(t) < need {
			return nil
		}
		ends := t[14:]
		starts := t[16+segX2:]
		deltas := t[16+2*segX2:]
		ranges := t[16+3*segX2:]
		for s := 0; s < segX2/2; s++ {
			end, start := rune(be.Uint16(ends[2*s:])), rune(be.Uint16(starts[2*s:]))
			delta := be.Uint16(deltas[2*s:])
			ro := int(be.Uint16(ranges[2*s:]))
			if end < start || start == 0xFFFF {
				continue
			}
			for c := start; c <= end; c++ {
				var gid uint16
				if ro == 0 {
					gid = uint16(c) + delta
				} else {
					p := 16 + 3*segX2 + 2*s + ro + 2*int(c-start)
					if p+2 > len(t) {
						break
					}
					gid = be.Uint16(t[p:])
					if gid != 0 {
						gid += delta
					}
				}
				add(c, gid)
			}
		}
	case 12:
		if len(t) < 16 {
			return nil
		}
		groups := int(be.Uint32(t[12:]))
		for g := 0; g < groups && 16+12*g+12 <= len(t); g++ {
			rec := t[16+12*g:]
			sc, ec, sg := be.Uint32(rec), be.Uint32(rec[4:]), be.Uint32(rec[8:])
			if ec < sc || ec-sc > 0x10FFFF {
				continue
			}
			for c := sc; c <= ec; c++ {
				add(rune(c), uint16(sg+(c-sc)))
			}
		}
	default:
		return nil
	}
	return out
}

// loadEmbeddedUnicode prepares a lazy CID → text fallback for a Type0 font
// with an embedded TrueType/OpenType program. The font program is only
// decoded the first time a glyph lacks a ToUnicode mapping.
func (f *Font) loadEmbeddedUnicode(r *pdf.Reader, cidFont, desc pdf.Dict) {
	if desc == nil {
		return
	}
	s := r.Stream(desc["FontFile2"])
	if s == nil {
		if s3 := r.Stream(desc["FontFile3"]); s3 != nil && r.Name(s3.Dict["Subtype"]) == "OpenType" {
			s = s3
		}
	}
	if s == nil {
		return
	}
	var (
		once     sync.Once
		gidText  map[uint16]string
		cidToGID []byte
	)
	load := func() {
		data, err := r.Decode(s)
		if err != nil {
			return
		}
		gidText = glyphTextFromSFNT(data)
		if m := r.Stream(cidFont["CIDToGIDMap"]); m != nil {
			cidToGID, _ = r.Decode(m)
		}
	}
	f.cidUni = func(cid uint32) string {
		once.Do(load)
		if gidText == nil {
			return ""
		}
		gid := uint16(cid)
		if cidToGID != nil {
			if int(cid)*2+1 >= len(cidToGID) {
				return ""
			}
			gid = binary.BigEndian.Uint16(cidToGID[cid*2:])
		}
		return gidText[gid]
	}
}
