// cutout makes a skin picture's background transparent.
//
//	go run ./cmd/cutout [-fill=false] [-erase x0,y0,x1,y1]... [-trim N] <in.png> <out.png> [<preview.png>]
//
// The back-view illustration (nik.png) has a checkerboard baked in where its
// background should be transparent. Every light grey pixel connected to the
// picture's border becomes transparent; the camera's dark outline stops the
// fill. Pixels along the fill's edge keep their own colour, unmixed from the
// background, with an alpha for how much of them was camera, so the outline
// stays smooth on any page. The size is kept, so coordinates measured on the
// original still hold. The preview, if named, is the result over magenta.
//
// -fill=false skips the fill, for a picture with real transparency already
// (top.png). -erase clears a rectangle, [x0,x1) x [y0,y1) in the original's
// pixels: the lens, in the top view. -trim N crops to what is left plus N
// pixels and prints the offset, which coordinates on the result subtract.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"
)

type rects []image.Rectangle

func (r *rects) String() string { return fmt.Sprint(*r) }
func (r *rects) Set(s string) error {
	var x0, y0, x1, y1 int
	if _, err := fmt.Sscanf(s, "%d,%d,%d,%d", &x0, &y0, &x1, &y1); err != nil {
		return err
	}
	*r = append(*r, image.Rect(x0, y0, x1, y1))
	return nil
}

func main() {
	fill := flag.Bool("fill", true, "make the light background connected to the border transparent")
	trim := flag.Int("trim", -1, "crop to the opaque pixels plus this margin")
	var erase rects
	flag.Var(&erase, "erase", "clear x0,y0,x1,y1 (repeatable)")
	flag.Parse()
	args := flag.Args()
	if len(args) < 2 {
		log.Fatal("usage: cutout [-fill=false] [-erase x0,y0,x1,y1]... [-trim N] <in.png> <out.png> [<preview.png>]")
	}
	f, err := os.Open(args[0])
	if err != nil {
		log.Fatal(err)
	}
	src, err := png.Decode(f)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}
	b := src.Bounds()
	img := image.NewNRGBA(b)
	draw.Draw(img, b, src, b.Min, draw.Src)
	w, h := b.Dx(), b.Dy()

	at := func(x, y int) (int, int, int) {
		i := img.PixOffset(x+b.Min.X, y+b.Min.Y)
		return int(img.Pix[i]), int(img.Pix[i+1]), int(img.Pix[i+2])
	}
	lum := func(r, g, b int) int { return (299*r + 587*g + 114*b) / 1000 }
	light := func(x, y int) bool {
		if !*fill {
			return false
		}
		r, g, bb := at(x, y)
		return lum(r, g, bb) >= 196 && max(r, g, bb)-min(r, g, bb) <= 28
	}
	for _, r := range erase {
		r = r.Add(b.Min).Intersect(b)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				img.Pix[img.PixOffset(x, y)+3] = 0
			}
		}
	}

	// The fill, from every light pixel on the border.
	bg := make([]bool, w*h)
	queue := make([]int, 0, w*h/4)
	push := func(x, y int) {
		if x < 0 || y < 0 || x >= w || y >= h || bg[y*w+x] || !light(x, y) {
			return
		}
		bg[y*w+x] = true
		queue = append(queue, y*w+x)
	}
	for x := 0; x < w; x++ {
		push(x, 0)
		push(x, h-1)
	}
	for y := 0; y < h; y++ {
		push(0, y)
		push(w-1, y)
	}
	for len(queue) > 0 {
		p := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		x, y := p%w, p/w
		push(x+1, y)
		push(x-1, y)
		push(x, y+1)
		push(x, y-1)
	}

	// Transparent background; an alpha for the pixels next to it.
	var n, edge int
	minX, minY, maxX, maxY := w, h, 0, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x+b.Min.X, y+b.Min.Y)
			if bg[y*w+x] {
				img.Pix[i+3] = 0
				n++
				continue
			}
			if img.Pix[i+3] > 8 {
				minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
			}
			// The background beside it, averaged, if there is any.
			var sr, sg, sb, k int
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					xx, yy := x+dx, y+dy
					if xx >= 0 && yy >= 0 && xx < w && yy < h && bg[yy*w+xx] {
						r, g, bb := at(xx, yy)
						sr, sg, sb, k = sr+r, sg+g, sb+bb, k+1
					}
				}
			}
			if k == 0 {
				continue
			}
			edge++
			br, bgc, bb := sr/k, sg/k, sb/k
			r, g, bl := at(x, y)
			// Outline ink is about 35; the background about lum(br, bgc, bb).
			bl0 := lum(br, bgc, bb)
			a := float64(bl0-lum(r, g, bl)) / float64(bl0-35)
			a = min(max(a, 0), 1)
			if a < 1 {
				un := func(c, bgv int) uint8 {
					if a == 0 {
						return 0
					}
					v := (float64(c) - (1-a)*float64(bgv)) / a
					return uint8(min(max(v, 0), 255))
				}
				img.Pix[i], img.Pix[i+1], img.Pix[i+2] = un(r, br), un(g, bgc), un(bl, bb)
			}
			img.Pix[i+3] = uint8(a * 255)
		}
	}
	fmt.Printf("%dx%d: %d background pixels (%.1f%%), %d edge pixels; camera within (%d,%d)-(%d,%d)\n",
		w, h, n, 100*float64(n)/float64(w*h), edge, minX, minY, maxX, maxY)

	write := func(path string, m image.Image) {
		out, err := os.Create(path)
		if err != nil {
			log.Fatal(err)
		}
		if err := png.Encode(out, m); err != nil {
			log.Fatal(err)
		}
		if err := out.Close(); err != nil {
			log.Fatal(err)
		}
	}
	var out image.Image = img
	if *trim >= 0 {
		r := image.Rect(minX-*trim, minY-*trim, maxX+1+*trim, maxY+1+*trim).Intersect(image.Rect(0, 0, w, h))
		out = img.SubImage(r.Add(b.Min))
		fmt.Printf("trimmed to %dx%d at offset (%d,%d)\n", r.Dx(), r.Dy(), r.Min.X, r.Min.Y)
	}
	write(args[1], out)
	if len(args) > 2 {
		ob := out.Bounds()
		pv := image.NewNRGBA(ob)
		draw.Draw(pv, ob, &image.Uniform{color.NRGBA{255, 0, 255, 255}}, image.Point{}, draw.Src)
		draw.Draw(pv, ob, out, ob.Min, draw.Over)
		write(args[2], pv)
	}
}
