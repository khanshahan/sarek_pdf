package font

import (
	"encoding/binary"

	"github.com/khanshahan/sarek_pdf/internal/pdf"
)

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

// loadEmbeddedUnicode builds CID → Unicode for a CIDFontType2 font with
// an embedded TrueType/OpenType program.
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
	data, err := r.Decode(s)
	if err != nil {
		return
	}
	gidUni := glyphUnicodeFromSFNT(data)
	if len(gidUni) == 0 {
		return
	}
	var cidToGID []byte
	if m := r.Stream(cidFont["CIDToGIDMap"]); m != nil {
		cidToGID, _ = r.Decode(m)
	}
	f.cidUni = func(cid uint32) rune {
		gid := uint16(cid)
		if cidToGID != nil {
			if int(cid)*2+1 >= len(cidToGID) {
				return 0
			}
			gid = binary.BigEndian.Uint16(cidToGID[cid*2:])
		}
		return gidUni[gid]
	}
}
