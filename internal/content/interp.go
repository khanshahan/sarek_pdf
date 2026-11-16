package content

import (
	"bytes"
	"context"
	"math"

	"github.com/khanshahan/sarek_pdf/internal/font"
	"github.com/khanshahan/sarek_pdf/internal/pdf"
	"github.com/khanshahan/sarek_pdf/model"
)

// Glyph is a positioned character in display space.
type Glyph struct {
	Text      string
	Origin    model.Point // baseline start
	End       model.Point // baseline end (origin + advance)
	Up        model.Point // vector from baseline to ascender line, |Up| = font size
	Size      float64
	Font      *font.Font
	Color     model.Color
	Invisible bool
	Space     bool // glyph is a space character
}

// Horizontal reports whether the glyph runs left-to-right without rotation.
func (g *Glyph) Horizontal() bool {
	dx, dy := g.End.X-g.Origin.X, g.End.Y-g.Origin.Y
	if dx <= 0 {
		return dx == 0 && dy == 0 && g.Up.X == 0 && g.Up.Y < 0
	}
	return math.Abs(dy) < 0.1*dx && g.Up.Y < 0
}

// BBox returns the glyph's axis-aligned bounding box.
func (g *Glyph) BBox() model.Rect {
	asc, desc := 0.8, -0.2
	if g.Font != nil {
		asc, desc = g.Font.Ascent, g.Font.Descent
	}
	// Normalise unusual ascent/descent values.
	if asc < 0.5 || asc > 1.2 {
		asc = 0.8
	}
	if desc > -0.05 || desc < -0.5 {
		desc = -0.2
	}
	if g.Font != nil && (g.Font.HangsBelow || g.Font.TeXSymbols && g.Text == "√") {
		// TeX extension glyphs are positioned by their top edge and hang
		// below the origin.
		asc, desc = 0.05, -1.2
	}
	pts := [4]model.Point{
		{X: g.Origin.X + g.Up.X*desc, Y: g.Origin.Y + g.Up.Y*desc},
		{X: g.End.X + g.Up.X*desc, Y: g.End.Y + g.Up.Y*desc},
		{X: g.Origin.X + g.Up.X*asc, Y: g.Origin.Y + g.Up.Y*asc},
		{X: g.End.X + g.Up.X*asc, Y: g.End.Y + g.Up.Y*asc},
	}
	r := model.Rect{X0: pts[0].X, Y0: pts[0].Y, X1: pts[0].X, Y1: pts[0].Y}
	for _, p := range pts[1:] {
		r.X0, r.Y0 = min(r.X0, p.X), min(r.Y0, p.Y)
		r.X1, r.Y1 = max(r.X1, p.X), max(r.Y1, p.Y)
	}
	return r
}

// ImagePlacement is an image drawn on the page.
type ImagePlacement struct {
	BBox   model.Rect
	Stream *pdf.Stream // image XObject or synthesised inline image
	Mask   bool        // ImageMask
	// CTM maps image space (unit square, origin bottom-left) to display
	// space. Pixel (px, py) of a W×H image is at (px/W, 1-py/H).
	CTM Matrix
}

// Result is the output of interpreting one page.
type Result struct {
	Width, Height float64
	Glyphs        []Glyph
	Paths         []model.Path
	Images        []ImagePlacement
}

// Options controls interpretation.
type Options struct {
	Images bool // record image placements
	Paths  bool // record vector paths
}

type gstate struct {
	ctm         Matrix
	fillColor   model.Color
	strokeColor model.Color
	lineWidth   float64
	font        *font.Font
	fontSize    float64
	charSpace   float64
	wordSpace   float64
	hscale      float64
	leading     float64
	rise        float64
	render      int
}

type operand struct {
	num   float64
	obj   pdf.Object
	isNum bool
}

// Interpreter executes content streams. Create one per page.
type Interpreter struct {
	r      *pdf.Reader
	fonts  *font.Loader
	forms  *FormCache
	opts   Options
	res    Result
	gs     gstate
	stack  []gstate
	tm     Matrix
	tlm    Matrix
	path   []model.Segment
	cur    model.Point
	start  model.Point
	ops    []operand
	depth  int
	active map[pdf.Ref]bool
	ctx    context.Context
	steps  int
}

// MaxOperators bounds the work done per page (defends against hostile files).
const MaxOperators = 20_000_000

// NewInterpreter creates an interpreter for one page. glyphBuf, if not
// nil, is reused as backing storage for Result.Glyphs.
func NewInterpreter(r *pdf.Reader, fonts *font.Loader, forms *FormCache, opts Options, glyphBuf []Glyph) *Interpreter {
	in := &Interpreter{r: r, fonts: fonts, forms: forms, opts: opts, active: map[pdf.Ref]bool{}}
	in.res.Glyphs = glyphBuf[:0]
	return in
}

// RunPage interprets a page and returns the collected result.
func (in *Interpreter) RunPage(ctx context.Context, p pdf.Page) (*Result, error) {
	in.ctx = ctx
	m, w, h := PageMatrix(p.CropBox, p.Rotate)
	in.res.Width, in.res.Height = w, h
	in.gs = gstate{ctm: m, lineWidth: 1, hscale: 1}
	data, err := in.r.Contents(p)
	if len(data) == 0 && err != nil {
		return &in.res, err
	}
	in.run(data, p.Resources)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return &in.res, nil
}

func (in *Interpreter) run(data []byte, res pdf.Dict) {
	p := pdf.NewParser(data, 0)
	in.ops = in.ops[:0]
	for {
		if in.steps++; in.steps > MaxOperators {
			return
		}
		if in.steps&0xFFF == 0 && in.ctx.Err() != nil {
			return
		}
		p.SkipWS()
		if p.Pos >= len(data) {
			return
		}
		c := data[p.Pos]
		if c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9') {
			in.ops = append(in.ops, operand{num: scanFloat(p), isNum: true})
			continue
		}
		o, err := p.ParseObject()
		if err != nil {
			return
		}
		kw, ok := o.(pdf.Keyword)
		if !ok {
			in.ops = append(in.ops, operand{obj: o})
			continue
		}
		if kw == "BI" {
			in.inlineImage(p, res)
			in.ops = in.ops[:0]
			continue
		}
		in.exec(kw, res)
		in.ops = in.ops[:0]
	}
}

func scanFloat(p *pdf.Parser) float64 {
	b := p.B
	neg := false
	for p.Pos < len(b) && (b[p.Pos] == '-' || b[p.Pos] == '+') {
		if b[p.Pos] == '-' {
			neg = !neg
		}
		p.Pos++
	}
	var v float64
	for p.Pos < len(b) && b[p.Pos] >= '0' && b[p.Pos] <= '9' {
		v = v*10 + float64(b[p.Pos]-'0')
		p.Pos++
	}
	if p.Pos < len(b) && b[p.Pos] == '.' {
		p.Pos++
		scale := 0.1
		for p.Pos < len(b) && b[p.Pos] >= '0' && b[p.Pos] <= '9' {
			v += float64(b[p.Pos]-'0') * scale
			scale *= 0.1
			p.Pos++
		}
	}
	for p.Pos < len(b) && pdf.IsRegular(b[p.Pos]) {
		p.Pos++
	}
	if neg {
		return -v
	}
	return v
}

func (in *Interpreter) num(i int) float64 {
	if i < len(in.ops) && in.ops[i].isNum {
		return in.ops[i].num
	}
	return 0
}

func (in *Interpreter) nums() []float64 {
	out := make([]float64, 0, len(in.ops))
	for _, o := range in.ops {
		if o.isNum {
			out = append(out, o.num)
		}
	}
	return out
}

func (in *Interpreter) need(n int) bool { return len(in.ops) >= n }

func (in *Interpreter) last() pdf.Object {
	if len(in.ops) == 0 {
		return nil
	}
	return in.ops[len(in.ops)-1].obj
}

func (in *Interpreter) exec(op pdf.Keyword, res pdf.Dict) {
	gs := &in.gs
	switch op {
	// Graphics state
	case "q":
		if len(in.stack) < 1024 {
			in.stack = append(in.stack, *gs)
		}
	case "Q":
		if n := len(in.stack); n > 0 {
			*gs = in.stack[n-1]
			in.stack = in.stack[:n-1]
		}
	case "cm":
		if in.need(6) {
			m := Matrix{in.num(0), in.num(1), in.num(2), in.num(3), in.num(4), in.num(5)}
			gs.ctm = m.Mul(gs.ctm)
		}
	case "w":
		if in.need(1) {
			gs.lineWidth = in.num(0)
		}
	case "gs":
		in.extGState(res)

	// Colour
	case "g":
		gs.fillColor = toRGB(in.nums())
	case "G":
		gs.strokeColor = toRGB(in.nums())
	case "rg", "k", "sc", "scn":
		if n := in.nums(); len(n) > 0 {
			gs.fillColor = toRGB(n)
		}
	case "RG", "K", "SC", "SCN":
		if n := in.nums(); len(n) > 0 {
			gs.strokeColor = toRGB(n)
		}
	case "cs":
		gs.fillColor = model.Color{}
	case "CS":
		gs.strokeColor = model.Color{}

	// Path construction
	case "m":
		if in.need(2) {
			in.cur = gs.ctm.Apply(in.num(0), in.num(1))
			in.start = in.cur
			in.addSeg(model.MoveTo, in.cur)
		}
	case "l":
		if in.need(2) {
			in.cur = gs.ctm.Apply(in.num(0), in.num(1))
			in.addSeg(model.LineTo, in.cur)
		}
	case "c":
		if in.need(6) {
			p1 := gs.ctm.Apply(in.num(0), in.num(1))
			p2 := gs.ctm.Apply(in.num(2), in.num(3))
			in.cur = gs.ctm.Apply(in.num(4), in.num(5))
			in.addSeg(model.CurveTo, p1, p2, in.cur)
		}
	case "v":
		if in.need(4) {
			p1 := in.cur
			p2 := gs.ctm.Apply(in.num(0), in.num(1))
			in.cur = gs.ctm.Apply(in.num(2), in.num(3))
			in.addSeg(model.CurveTo, p1, p2, in.cur)
		}
	case "y":
		if in.need(4) {
			p1 := gs.ctm.Apply(in.num(0), in.num(1))
			in.cur = gs.ctm.Apply(in.num(2), in.num(3))
			in.addSeg(model.CurveTo, p1, in.cur, in.cur)
		}
	case "h":
		in.addSeg(model.Close)
		in.cur = in.start
	case "re":
		if in.need(4) {
			x, y, w, h := in.num(0), in.num(1), in.num(2), in.num(3)
			in.start = gs.ctm.Apply(x, y)
			in.addSeg(model.MoveTo, in.start)
			in.addSeg(model.LineTo, gs.ctm.Apply(x+w, y))
			in.addSeg(model.LineTo, gs.ctm.Apply(x+w, y+h))
			in.addSeg(model.LineTo, gs.ctm.Apply(x, y+h))
			in.addSeg(model.Close)
			in.cur = in.start
		}

	// Path painting
	case "S":
		in.paint(true, false, false)
	case "s":
		in.addSeg(model.Close)
		in.paint(true, false, false)
	case "f", "F":
		in.paint(false, true, false)
	case "f*":
		in.paint(false, true, true)
	case "B":
		in.paint(true, true, false)
	case "B*":
		in.paint(true, true, true)
	case "b":
		in.addSeg(model.Close)
		in.paint(true, true, false)
	case "b*":
		in.addSeg(model.Close)
		in.paint(true, true, true)
	case "n":
		in.path = in.path[:0]

	// Text
	case "BT":
		in.tm, in.tlm = Identity, Identity
	case "ET":
	case "Tc":
		gs.charSpace = in.num(0)
	case "Tw":
		gs.wordSpace = in.num(0)
	case "Tz":
		gs.hscale = in.num(0) / 100
	case "TL":
		gs.leading = in.num(0)
	case "Ts":
		gs.rise = in.num(0)
	case "Tr":
		gs.render = int(in.num(0))
	case "Tf":
		if in.need(2) {
			name, _ := in.ops[0].obj.(pdf.Name)
			gs.fontSize = in.num(1)
			fonts := in.r.Dict(res["Font"])
			gs.font = in.fonts.Get(fonts[name])
		}
	case "Td":
		if in.need(2) {
			in.tlm = Matrix{1, 0, 0, 1, in.num(0), in.num(1)}.Mul(in.tlm)
			in.tm = in.tlm
		}
	case "TD":
		if in.need(2) {
			gs.leading = -in.num(1)
			in.tlm = Matrix{1, 0, 0, 1, in.num(0), in.num(1)}.Mul(in.tlm)
			in.tm = in.tlm
		}
	case "Tm":
		if in.need(6) {
			in.tlm = Matrix{in.num(0), in.num(1), in.num(2), in.num(3), in.num(4), in.num(5)}
			in.tm = in.tlm
		}
	case "T*":
		in.nextLine()
	case "Tj":
		if s, ok := in.last().(pdf.String); ok {
			in.showText([]byte(s))
		}
	case "'":
		in.nextLine()
		if s, ok := in.last().(pdf.String); ok {
			in.showText([]byte(s))
		}
	case "\"":
		if in.need(3) {
			gs.wordSpace, gs.charSpace = in.num(0), in.num(1)
			in.nextLine()
			if s, ok := in.ops[2].obj.(pdf.String); ok {
				in.showText([]byte(s))
			}
		}
	case "TJ":
		arr, _ := in.last().(pdf.Array)
		for _, e := range arr {
			switch v := e.(type) {
			case pdf.String:
				in.showText([]byte(v))
			case pdf.Integer, pdf.Real:
				n, _ := pdf.Num(v)
				tx := -n / 1000 * gs.fontSize * gs.hscale
				in.tm = Matrix{1, 0, 0, 1, tx, 0}.Mul(in.tm)
			}
		}

	// XObjects
	case "Do":
		if name, ok := in.last().(pdf.Name); ok {
			in.doXObject(name, res)
		}
	}
}

func (in *Interpreter) nextLine() {
	in.tlm = Matrix{1, 0, 0, 1, 0, -in.gs.leading}.Mul(in.tlm)
	in.tm = in.tlm
}

func (in *Interpreter) addSeg(op model.SegOp, pts ...model.Point) {
	if !in.opts.Paths {
		return
	}
	if len(in.path) > 200_000 {
		return
	}
	s := model.Segment{Op: op}
	copy(s.Pts[:], pts)
	in.path = append(in.path, s)
}

func (in *Interpreter) paint(stroke, fill, evenOdd bool) {
	defer func() { in.path = in.path[:0] }()
	if !in.opts.Paths || len(in.path) == 0 {
		return
	}
	segs := make([]model.Segment, len(in.path))
	copy(segs, in.path)
	bb := model.Rect{X0: math.Inf(1), Y0: math.Inf(1), X1: math.Inf(-1), Y1: math.Inf(-1)}
	for _, s := range segs {
		n := 1
		switch s.Op {
		case model.Close:
			n = 0
		case model.CurveTo:
			n = 3
		}
		for k := 0; k < n; k++ {
			p := s.Pts[k]
			bb.X0, bb.Y0 = min(bb.X0, p.X), min(bb.Y0, p.Y)
			bb.X1, bb.Y1 = max(bb.X1, p.X), max(bb.Y1, p.Y)
		}
	}
	if math.IsInf(bb.X0, 0) {
		return
	}
	lw := in.gs.lineWidth * in.gs.ctm.Scale()
	if lw <= 0 {
		lw = 0.5 // zero-width lines render as thinnest line
	}
	in.res.Paths = append(in.res.Paths, model.Path{
		Segments: segs, Stroke: stroke, Fill: fill, EvenOdd: evenOdd,
		StrokeColor: in.gs.strokeColor, FillColor: in.gs.fillColor,
		LineWidth: lw, BBox: bb,
	})
}

func (in *Interpreter) showText(s []byte) {
	gs := &in.gs
	f := gs.font
	if f == nil {
		f = in.fonts.Get(nil)
		gs.font = f
	}
	size := gs.fontSize
	invisible := gs.render == 3 || gs.render == 7
	f.Decode(s, func(g font.Glyph) {
		trm := Matrix{size * gs.hscale, 0, 0, size, 0, gs.rise}.Mul(in.tm).Mul(gs.ctm)
		w0 := g.Width
		if f.Vertical {
			origin := trm.Apply(0, 0)
			up := trm.ApplyVec(0, 1)
			in.emit(g, origin, trm.Apply(1, 0), up, invisible)
			ty := -(1.0*size + gs.charSpace)
			if g.Space {
				ty -= gs.wordSpace
			}
			in.tm = Matrix{1, 0, 0, 1, 0, ty}.Mul(in.tm)
			return
		}
		origin := trm.Apply(0, 0)
		end := trm.Apply(w0, 0)
		up := trm.ApplyVec(0, 1)
		in.emit(g, origin, end, up, invisible)
		tx := w0*size + gs.charSpace
		if g.Space {
			tx += gs.wordSpace
		}
		tx *= gs.hscale
		in.tm = Matrix{1, 0, 0, 1, tx, 0}.Mul(in.tm)
	})
}

func (in *Interpreter) emit(g font.Glyph, origin, end, up model.Point, invisible bool) {
	sz := math.Hypot(up.X, up.Y)
	if sz < 0.1 || len(in.res.Glyphs) > 5_000_000 {
		return
	}
	in.res.Glyphs = append(in.res.Glyphs, Glyph{
		Text: g.Text, Origin: origin, End: end, Up: up, Size: sz,
		Font: in.gs.font, Color: in.gs.fillColor, Invisible: invisible,
		Space: g.Space || g.Text == " ",
	})
}

func (in *Interpreter) extGState(res pdf.Dict) {
	name, ok := in.last().(pdf.Name)
	if !ok {
		return
	}
	gsd := in.r.Dict(in.r.Dict(res["ExtGState"])[name])
	if gsd == nil {
		return
	}
	if lw, ok := pdf.Num(in.r.Resolve(gsd["LW"])); ok {
		in.gs.lineWidth = lw
	}
	if fa := in.r.Array(gsd["Font"]); len(fa) == 2 {
		in.gs.font = in.fonts.Get(fa[0])
		in.gs.fontSize = in.r.Num(fa[1], in.gs.fontSize)
	}
}

func (in *Interpreter) doXObject(name pdf.Name, res pdf.Dict) {
	xobjs := in.r.Dict(res["XObject"])
	ref, _ := xobjs[name].(pdf.Ref)
	s := in.r.Stream(xobjs[name])
	if s == nil {
		return
	}
	switch in.r.Name(s.Dict["Subtype"]) {
	case "Image":
		if in.opts.Images {
			in.addImage(s)
		}
	case "Form":
		if in.depth >= 24 || in.active[ref] {
			return
		}
		data := in.forms.Get(in.r, s)
		if data == nil {
			return
		}
		saved := in.gs
		savedStack := len(in.stack)
		savedTm, savedTlm := in.tm, in.tlm
		if m := in.r.Numbers(s.Dict["Matrix"]); len(m) == 6 {
			in.gs.ctm = Matrix{m[0], m[1], m[2], m[3], m[4], m[5]}.Mul(in.gs.ctm)
		}
		formRes := in.r.Dict(s.Dict["Resources"])
		if formRes == nil {
			formRes = res
		}
		in.active[ref] = true
		in.depth++
		ops := in.ops
		in.ops = nil
		in.run(data, formRes)
		in.ops = ops[:0]
		in.depth--
		delete(in.active, ref)
		in.gs = saved
		in.stack = in.stack[:min(savedStack, len(in.stack))]
		in.tm, in.tlm = savedTm, savedTlm
	}
}

func (in *Interpreter) addImage(s *pdf.Stream) {
	// Image space is the unit square mapped by the CTM.
	ctm := in.gs.ctm
	pts := [4]model.Point{ctm.Apply(0, 0), ctm.Apply(1, 0), ctm.Apply(0, 1), ctm.Apply(1, 1)}
	bb := model.Rect{X0: pts[0].X, Y0: pts[0].Y, X1: pts[0].X, Y1: pts[0].Y}
	for _, p := range pts[1:] {
		bb.X0, bb.Y0 = min(bb.X0, p.X), min(bb.Y0, p.Y)
		bb.X1, bb.Y1 = max(bb.X1, p.X), max(bb.Y1, p.Y)
	}
	mask, _ := in.r.Resolve(s.Dict["ImageMask"]).(pdf.Boolean)
	in.res.Images = append(in.res.Images, ImagePlacement{BBox: bb, Stream: s, Mask: bool(mask), CTM: ctm})
}

var inlineKeys = map[pdf.Name]pdf.Name{
	"BPC": "BitsPerComponent", "CS": "ColorSpace", "D": "Decode", "DP": "DecodeParms",
	"F": "Filter", "H": "Height", "IM": "ImageMask", "I": "Interpolate", "W": "Width",
}

var inlineValues = map[pdf.Name]pdf.Name{
	"G": "DeviceGray", "RGB": "DeviceRGB", "CMYK": "DeviceCMYK", "I": "Indexed",
	"AHx": "ASCIIHexDecode", "A85": "ASCII85Decode", "LZW": "LZWDecode", "Fl": "FlateDecode",
	"RL": "RunLengthDecode", "CCF": "CCITTFaxDecode", "DCT": "DCTDecode",
}

func (in *Interpreter) inlineImage(p *pdf.Parser, res pdf.Dict) {
	d := pdf.Dict{}
	for {
		o, err := p.ParseObject()
		if err != nil {
			return
		}
		if kw, ok := o.(pdf.Keyword); ok {
			if kw == "ID" {
				break
			}
			continue
		}
		k, ok := o.(pdf.Name)
		if !ok {
			continue
		}
		v, err := p.ParseObject()
		if err != nil {
			return
		}
		if full, ok := inlineKeys[k]; ok {
			k = full
		}
		if n, ok := v.(pdf.Name); ok {
			if full, ok := inlineValues[n]; ok {
				v = full
			}
		}
		d[k] = v
	}
	// One whitespace byte follows ID.
	if p.Pos < len(p.B) {
		p.Pos++
	}
	start := p.Pos
	end := findEI(p.B, start)
	data := p.B[start:end]
	p.Pos = min(end+2, len(p.B))
	if in.opts.Images {
		in.addImage(&pdf.Stream{Dict: d, Raw: data})
	}
}

// findEI locates the "EI" operator that ends inline image data.
func findEI(b []byte, from int) int {
	for i := from; i+1 < len(b); {
		k := bytes.Index(b[i:], []byte("EI"))
		if k < 0 {
			return len(b)
		}
		at := i + k
		before := at == 0 || pdf.IsWhite(b[at-1])
		after := at+2 >= len(b) || pdf.IsWhite(b[at+2])
		if before && after {
			e := at
			if e > from && pdf.IsWhite(b[e-1]) {
				e--
			}
			return e
		}
		i = at + 2
	}
	return len(b)
}

func toRGB(n []float64) model.Color {
	c := func(v float64) uint8 {
		v = math.Max(0, math.Min(1, v))
		return uint8(v*255 + 0.5)
	}
	switch len(n) {
	case 1:
		return model.Color{R: c(n[0]), G: c(n[0]), B: c(n[0])}
	case 3:
		return model.Color{R: c(n[0]), G: c(n[1]), B: c(n[2])}
	case 4:
		k := n[3]
		return model.Color{R: c((1 - n[0]) * (1 - k)), G: c((1 - n[1]) * (1 - k)), B: c((1 - n[2]) * (1 - k))}
	}
	return model.Color{}
}
