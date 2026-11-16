package content

import (
	"sync"

	"github.com/khanshahan/sarek_pdf/internal/pdf"
)

// FormCache caches decoded form XObject content per document so that
// forms reused across pages (logos, headers) are decoded once.
// It is safe for concurrent use and bounded in total size.
type FormCache struct {
	mu    sync.Mutex
	m     map[pdf.Ref][]byte
	bytes int
	limit int
}

// NewFormCache returns a cache holding at most limit bytes.
func NewFormCache(limit int) *FormCache {
	return &FormCache{m: map[pdf.Ref][]byte{}, limit: limit}
}

// Get returns the decoded content of a form stream.
func (c *FormCache) Get(r *pdf.Reader, s *pdf.Stream) []byte {
	cacheable := s.Ref != (pdf.Ref{})
	if cacheable {
		c.mu.Lock()
		d, ok := c.m[s.Ref]
		c.mu.Unlock()
		if ok {
			return d
		}
	}
	data, err := r.Decode(s)
	if err != nil && len(data) == 0 {
		return nil
	}
	if cacheable {
		c.mu.Lock()
		if c.bytes+len(data) <= c.limit {
			c.m[s.Ref] = data
			c.bytes += len(data)
		}
		c.mu.Unlock()
	}
	return data
}
