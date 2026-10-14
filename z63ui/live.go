package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// The scanout-live file (nikon-z63-scanout.h, "scanout-live"); the top
// LCD's toplcd-live (nikon-z63-toplcd.h) has the same layout with room for
// its own 128x128.
const (
	liveMagic  = "Z63LIVE1"
	liveHeader = 4096
	liveSeq    = 16
	liveNote   = 48
)

// frame is the last picture read from the file, as RGBA.
type frame struct {
	seq    uint64
	w, h   int
	rgba   []byte
	note   string
	frames uint64
}

// live follows one of the current run's live files and serves it to the
// camera page: frame://localhost/next?after=<seq> for the scanout,
// frame://localhost/top/next for the control panel (main.go).
type live struct {
	mu   sync.Mutex
	path string
	data []byte // the mapping, nil until QEMU has made the file
	cur  frame
	wake chan struct{}
	stop chan struct{}
}

func newLive() *live { return &live{wake: make(chan struct{})} }

// follow switches to a new run's file: the old mapping goes before QEMU
// truncates anything, and the last picture stays until a new one comes.
func (l *live) follow(path string) {
	l.mu.Lock()
	if l.stop != nil {
		close(l.stop)
	}
	if l.data != nil {
		syscall.Munmap(l.data)
		l.data = nil
	}
	l.path = path
	stop := make(chan struct{})
	l.stop = stop
	l.mu.Unlock()
	go l.poll(path, stop)
}

// release unmaps the file (the run is over and it may be removed).
func (l *live) release() {
	l.mu.Lock()
	if l.stop != nil {
		close(l.stop)
		l.stop = nil
	}
	if l.data != nil {
		syscall.Munmap(l.data)
		l.data = nil
	}
	l.mu.Unlock()
}

var fenceWord uint64

func (l *live) poll(path string, stop chan struct{}) {
	var last uint64 = ^uint64(0)
	var lastFrames uint64
	px := make([]byte, 0, 1024*682*4)
	tick := time.NewTicker(8 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		l.mu.Lock()
		data := l.data
		l.mu.Unlock()
		if data == nil {
			data = mapLive(path)
			if data == nil {
				continue
			}
			l.mu.Lock()
			if l.stop != stop { // switched away meanwhile
				l.mu.Unlock()
				syscall.Munmap(data)
				return
			}
			l.data = data
			l.mu.Unlock()
		}
		seqp := (*uint64)(unsafe.Pointer(&data[liveSeq]))
		framesNow := atomic.LoadUint64((*uint64)(unsafe.Pointer(&data[32])))
		s1 := atomic.LoadUint64(seqp)
		if s1 == last || s1&1 == 1 {
			if framesNow != lastFrames {
				lastFrames = framesNow
				l.mu.Lock()
				l.cur.frames = framesNow
				l.mu.Unlock()
			}
			continue
		}
		w := int(binary.LittleEndian.Uint32(data[24:]))
		h := int(binary.LittleEndian.Uint32(data[28:]))
		if liveHeader+w*h*4 > len(data) {
			continue
		}
		note := data[liveNote : liveNote+200]
		if i := bytes.IndexByte(note, 0); i >= 0 {
			note = note[:i]
		}
		noteS := string(note)
		px = append(px[:0], data[liveHeader:liveHeader+w*h*4]...)
		atomic.AddUint64(&fenceWord, 0) // the copy before the second read
		if atomic.LoadUint64(seqp) != s1 {
			continue // QEMU wrote meanwhile: read it again next tick
		}
		last, lastFrames = s1, framesNow
		rgba := make([]byte, w*h*4)
		for i := 0; i < w*h*4; i += 4 { // u32 0x00RRGGBB, little-endian
			rgba[i], rgba[i+1], rgba[i+2], rgba[i+3] = px[i+2], px[i+1], px[i], 255
		}
		l.mu.Lock()
		l.cur = frame{seq: s1, w: w, h: h, rgba: rgba, note: noteS, frames: framesNow}
		close(l.wake)
		l.wake = make(chan struct{})
		l.mu.Unlock()
	}
}

func mapLive(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() < liveHeader {
		return nil
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(st.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil
	}
	// The magic goes in last, after the sizes; the file is sized before.
	capacity := int(binary.LittleEndian.Uint32(data[12:]))
	if string(data[:8]) != liveMagic || liveHeader+capacity > len(data) {
		syscall.Munmap(data)
		return nil
	}
	return data[:liveHeader+capacity]
}

// current returns the last frame.
func (l *live) current() frame {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cur
}

// ServeHTTP answers frame://localhost/next?after=<seq>: as soon as the
// picture differs from <seq>, or after a second with no pixels, a
// 36-byte header -- "Z63F", u32 flags (1: RGBA pixels follow), u64 seq,
// u32 width, u32 height, u64 frames composed, u32 note length -- then the
// note and the pixels. frame://localhost/png is the picture as a PNG.
func (l *live) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/png" {
		f := l.current()
		if f.w == 0 {
			http.Error(w, "no frame yet", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		png.Encode(w, rgbaImage(f))
		return
	}
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	l.mu.Lock()
	f, wake := l.cur, l.wake
	l.mu.Unlock()
	pixels := f.seq != after && f.w > 0
	if !pixels {
		select {
		case <-wake:
			f = l.current()
			pixels = f.w > 0
		case <-time.After(time.Second):
			f = l.current()
		case <-r.Context().Done():
			return
		}
	}
	hdr := make([]byte, 36, 36+len(f.note))
	copy(hdr, "Z63F")
	if pixels {
		binary.LittleEndian.PutUint32(hdr[4:], 1)
	}
	binary.LittleEndian.PutUint64(hdr[8:], f.seq)
	binary.LittleEndian.PutUint32(hdr[16:], uint32(f.w))
	binary.LittleEndian.PutUint32(hdr[20:], uint32(f.h))
	binary.LittleEndian.PutUint64(hdr[24:], f.frames)
	binary.LittleEndian.PutUint32(hdr[32:], uint32(len(f.note)))
	hdr = append(hdr, f.note...)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(hdr)
	if pixels {
		w.Write(f.rgba)
	}
}

func rgbaImage(f frame) *image.RGBA {
	return &image.RGBA{Pix: f.rgba, Stride: f.w * 4, Rect: image.Rect(0, 0, f.w, f.h)}
}
