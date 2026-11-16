package model

// Merged returns the complete table formed by t and all of its
// continuation parts on following pages (t itself when it has none).
// Repeated header rows of the continuation parts are dropped. The column
// grid and bounding box of the first part are used. The parts are not
// modified.
//
// Calling Merged on a continuation part returns the table from that part
// onwards; exporters should start from the first part (Prev == nil).
func (t *Table) Merged() *Table {
	if t.Next == nil {
		return t
	}
	m := &Table{BBox: t.BBox, Ruled: t.Ruled, ColX: t.ColX, RowY: append([]float64(nil), t.RowY...)}
	for _, c := range t.Cells {
		cc := *c
		m.Cells = append(m.Cells, &cc)
	}
	seen := map[*Table]bool{t: true}
	for part := t.Next; part != nil && !seen[part]; part = part.Next {
		seen[part] = true
		off := m.NumRows() - part.HeaderRows
		for _, c := range part.Cells {
			if c.Row < part.HeaderRows {
				continue // repeated header
			}
			cc := *c
			cc.Row += off
			if cc.Col+cc.ColSpan > m.NumCols() {
				cc.ColSpan = max(1, m.NumCols()-cc.Col)
			}
			if cc.Col < m.NumCols() {
				m.Cells = append(m.Cells, &cc)
			}
		}
		last := m.RowY[len(m.RowY)-1]
		for i := part.HeaderRows; i+1 < len(part.RowY); i++ {
			last += part.RowY[i+1] - part.RowY[i]
			m.RowY = append(m.RowY, last)
		}
	}
	return m
}
