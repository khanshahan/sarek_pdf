// Package content interprets PDF content streams into positioned glyphs,
// vector paths and image placements in display space.
package content

import (
	"math"

	"github.com/khanshahan/sarek_pdf/model"
)

// Matrix is an affine transform [a b c d e f]; a point (x,y) maps to
// (a*x + c*y + e, b*x + d*y + f).
type Matrix [6]float64

// Identity is the identity matrix.
var Identity = Matrix{1, 0, 0, 1, 0, 0}

// Mul returns m×n (apply m first, then n).
func (m Matrix) Mul(n Matrix) Matrix {
	return Matrix{
		m[0]*n[0] + m[1]*n[2],
		m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2],
		m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4],
		m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

// Apply transforms a point.
func (m Matrix) Apply(x, y float64) model.Point {
	return model.Point{X: m[0]*x + m[2]*y + m[4], Y: m[1]*x + m[3]*y + m[5]}
}

// ApplyVec transforms a vector (no translation).
func (m Matrix) ApplyVec(x, y float64) model.Point {
	return model.Point{X: m[0]*x + m[2]*y, Y: m[1]*x + m[3]*y}
}

// Scale returns the geometric mean scale factor.
func (m Matrix) Scale() float64 { return math.Sqrt(math.Abs(m[0]*m[3] - m[1]*m[2])) }

// PageMatrix maps PDF user space of a page to display space (top-left
// origin, y down, rotation applied). It returns the matrix and the
// display width and height.
func PageMatrix(crop [4]float64, rotate int) (Matrix, float64, float64) {
	x0, y0, x1, y1 := crop[0], crop[1], crop[2], crop[3]
	w, h := x1-x0, y1-y0
	switch rotate {
	case 90:
		return Matrix{0, 1, 1, 0, -y0, -x0}, h, w
	case 180:
		return Matrix{-1, 0, 0, 1, x1, -y0}, w, h
	case 270:
		return Matrix{0, -1, -1, 0, y1, x1}, h, w
	}
	return Matrix{1, 0, 0, -1, -x0, y1}, w, h
}
