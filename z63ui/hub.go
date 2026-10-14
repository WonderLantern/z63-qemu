package main

import (
	"context"
	"regexp"
	"sort"
	"sync"
	"time"
)

// hub keeps the last items of a stream (stderr lines, UART text) for pages
// that follow it. Adding never waits on a page: QEMU's stderr ran to 268k
// lines in ten minutes in run n9, and a page that falls behind is told how
// many items it missed instead.
type hub struct {
	mu     sync.Mutex
	items  []string
	first  int // index of items[0] in the whole stream
	bytes  int
	limit  int // bytes kept
	run    string
	starts map[int]string // stream index -> run that starts there
	wake   chan struct{}
	tags   map[string]int
	tagged bool // count lines by tag (stderr)
	chunks bool // items are text chunks, not lines (the UART)
}

func newHub(limit int, tagged, chunks bool) *hub {
	return &hub{limit: limit, wake: make(chan struct{}), starts: map[int]string{}, tags: map[string]int{}, tagged: tagged, chunks: chunks}
}

// tagRe takes the leading tag of a QEMU stderr line: Z63_SCANOUT, Z9_150C_STOP ...
var tagRe = regexp.MustCompile(`^(?:qemu-system-aarch64: )?([A-Z][A-Z0-9_]{2,})\b`)

func (h *hub) begin(run string) {
	h.mu.Lock()
	h.run = run
	h.starts[h.first+len(h.items)] = run
	h.tags = map[string]int{}
	h.mu.Unlock()
}

func (h *hub) add(s string) {
	h.mu.Lock()
	h.items = append(h.items, s)
	h.bytes += len(s) + 16
	if h.tagged {
		tag := "(other)"
		if m := tagRe.FindStringSubmatch(s); m != nil {
			tag = m[1]
		}
		h.tags[tag]++
	}
	// Trim in bulk, to three quarters of the limit, so a full hub does not
	// copy itself for every line.
	n := 0
	if h.bytes > h.limit {
		for h.bytes > h.limit*3/4 && n < len(h.items)-1 {
			h.bytes -= len(h.items[n]) + 16
			n++
		}
	}
	if n > 0 {
		h.items = append(h.items[:0:0], h.items[n:]...)
		for i := range h.starts {
			if i < h.first+n {
				delete(h.starts, i)
			}
		}
		h.first += n
	}
	close(h.wake)
	h.wake = make(chan struct{})
	h.mu.Unlock()
}

// Follow sends the last backlog items (bytes, for a hub of chunks) and
// then new ones, in batches, until ctx ends or send fails.
func (h *hub) Follow(ctx context.Context, backlog int, send func(LogBatch) error) error {
	h.mu.Lock()
	cur := h.first + len(h.items)
	if !h.chunks {
		cur = max(h.first, cur-backlog)
	} else {
		for b := 0; cur > h.first && b < backlog; {
			cur--
			b += len(h.items[cur-h.first])
		}
	}
	h.mu.Unlock()
	for {
		h.mu.Lock()
		for cur == h.first+len(h.items) {
			wake := h.wake
			h.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil
			case <-wake:
			}
			h.mu.Lock()
		}
		b := LogBatch{Run: h.run}
		if cur < h.first {
			b.Dropped = h.first - cur
			cur = h.first
		}
		end := min(h.first+len(h.items), cur+2000)
		// A batch never spans two runs: the page starts afresh at Reset.
		for i := cur; i < end; i++ {
			if run, ok := h.starts[i]; ok {
				if i == cur {
					b.Reset, b.Run = true, run
				} else {
					end = i
					break
				}
			}
		}
		b.Items = append([]string(nil), h.items[cur-h.first:end-h.first]...)
		cur = end
		h.mu.Unlock()
		if err := send(b); err != nil {
			return nil
		}
		// Let a burst gather into the next batch.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(80 * time.Millisecond):
		}
	}
}

// TagCount is one row of the stderr tag table.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// Tags returns the current run's lines per tag, most first.
func (h *hub) Tags() []TagCount {
	h.mu.Lock()
	out := make([]TagCount, 0, len(h.tags))
	for t, n := range h.tags {
		out = append(out, TagCount{t, n})
	}
	h.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Tag < out[j].Tag
	})
	return out
}
