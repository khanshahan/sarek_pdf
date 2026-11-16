package sarekpdf

import (
	"github.com/khanshahan/sarek_pdf/internal/pdf"
	"github.com/khanshahan/sarek_pdf/model"
)

// Outline returns the document's bookmarks (author-defined headings).
func (d *Document) Outline() []model.OutlineItem {
	root := d.r.Dict(d.r.Catalog()["Outlines"])
	if root == nil {
		return nil
	}
	pageIdx := make(map[pdf.Ref]int, len(d.pages))
	for i, p := range d.pages {
		pageIdx[p.Ref] = i + 1
	}
	seen := map[pdf.Ref]bool{}
	count := 0
	var walk func(first pdf.Object, depth int) []model.OutlineItem
	walk = func(first pdf.Object, depth int) []model.OutlineItem {
		var items []model.OutlineItem
		for cur := first; cur != nil && depth < 32 && count < 100_000; {
			ref, ok := cur.(pdf.Ref)
			if ok {
				if seen[ref] {
					break
				}
				seen[ref] = true
			}
			n := d.r.Dict(cur)
			if n == nil {
				break
			}
			count++
			item := model.OutlineItem{Title: pdf.TextString(d.r.Resolve(n["Title"]))}
			dest := n["Dest"]
			if dest == nil {
				if a := d.r.Dict(n["A"]); a != nil && d.r.Name(a["S"]) == "GoTo" {
					dest = a["D"]
				}
			}
			item.Page = d.destPage(dest, pageIdx)
			item.Children = walk(n["First"], depth+1)
			items = append(items, item)
			cur = n["Next"]
		}
		return items
	}
	return walk(root["First"], 0)
}

func (d *Document) destPage(dest pdf.Object, pageIdx map[pdf.Ref]int) int {
	for i := 0; i < 4; i++ {
		switch v := d.r.Resolve(dest).(type) {
		case pdf.Array:
			if len(v) == 0 {
				return 0
			}
			if ref, ok := v[0].(pdf.Ref); ok {
				return pageIdx[ref]
			}
			if n, ok := pdf.Int(v[0]); ok && n >= 0 && n < len(d.pages) {
				return n + 1 // remote-style page index
			}
			return 0
		case pdf.Dict:
			dest = v["D"]
		case pdf.Name:
			dest = d.r.Dict(d.r.Catalog()["Dests"])[v]
		case pdf.String:
			dest = d.lookupNameTree(d.r.Dict(d.r.Dict(d.r.Catalog()["Names"])["Dests"]), string(v), 0)
		default:
			return 0
		}
	}
	return 0
}

func (d *Document) lookupNameTree(node pdf.Dict, key string, depth int) pdf.Object {
	if node == nil || depth > 32 {
		return nil
	}
	if names := d.r.Array(node["Names"]); names != nil {
		for i := 0; i+1 < len(names); i += 2 {
			if s, ok := d.r.Resolve(names[i]).(pdf.String); ok && string(s) == key {
				return names[i+1]
			}
		}
		return nil
	}
	for _, kid := range d.r.Array(node["Kids"]) {
		k := d.r.Dict(kid)
		if lim := d.r.Array(k["Limits"]); len(lim) == 2 {
			lo, _ := d.r.Resolve(lim[0]).(pdf.String)
			hi, _ := d.r.Resolve(lim[1]).(pdf.String)
			if key < string(lo) || key > string(hi) {
				continue
			}
		}
		if v := d.lookupNameTree(k, key, depth+1); v != nil {
			return v
		}
	}
	return nil
}
