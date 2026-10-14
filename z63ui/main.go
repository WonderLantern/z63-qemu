// z63ui runs the Z 6III machine as a camera on the desktop.
//
// One window, one page: the body seen from the back, with the scanout on its
// monitor -- a touch screen -- and its buttons (pressed through qom-set
// /machine mcu-keys, as keys.py does); beside it the body seen from above,
// with the control panel the body MCU draws, and the serial console -- the
// RTOS and Linux share the UART, and with a DEBUG li image the BusyBox
// shell answers there. The pictures are in skin/ (make.sh).
// Under them, a dock with QEMU's own stderr, the firmwares' logs
// (z63-tools/plugins/libz63log), the QEMU monitor and the run's files.
//
// QEMU is a child process, started the way run2.sh starts it, and not a
// library loaded into this one: it has no library build, MyGo calls AppKit
// through purego (no cgo), so an in-process QEMU would be one too, sharing
// signals and the main thread with the Go runtime; and every exit() in it --
// error_fatal, the board's refusals with the default -action -- would close
// the windows. As a child it can be killed and started again with a new
// build while the windows and their logs stay. The scanout reaches this
// process through -machine scanout-live= (nikon-z63-scanout.h), a file both
// map, and the control panel through toplcd-live= (nikon-z63-toplcd.h); this
// build of QEMU has no VNC.
//
//	z63ui [-dir /Volumes/ExAPFS/z63-run] [-qemu ...] [-snor ...] [-nand ...]
//	      [-machine sio-sink=9:8:7] [-no-start] [-- extra QEMU arguments]
//
// Each run writes <out>/runs/<name>.serial (the UART), .log (stderr) and
// .z63log; <out>/ctl.sock takes one-line commands for scripts (ctl.go).
//
// The machine is in the qemu-z63 tree (branch mcu-link); paths here such as
// hw/arm/..., z63-tools/... and run2.sh are in that tree or its run
// directory, and its build needs scanout-live and toplcd-live
// (hw/arm/nikon-z63-scanout.h, nikon-z63-toplcd.h).
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// Config says where everything is. The defaults are the ExAPFS layout that
// run2.sh and session.sh use.
type Config struct {
	QEMU     string   `json:"qemu"`
	Dir      string   `json:"dir"`      // QEMU's working directory
	Firmware string   `json:"firmware"` // holds eg2010_0200i1.bi and ex2010_0200a0.bi
	Loader   string   `json:"loader"`
	SNOR     string   `json:"snor"`
	NAND     string   `json:"nand"` // copied to <out>/nand-ui.img before each run
	Raw      string   `json:"raw"`  // z9-capture-raw
	Machine  string   `json:"machine"`
	Plugin   string   `json:"plugin"` // libz63log; empty for none
	Gdb      string   `json:"gdb"`
	Out      string   `json:"out"`
	Extra    []string `json:"extra"`
	NoStart  bool     `json:"noStart"`
}

func parseConfig(args []string) Config {
	const run = "/Volumes/ExAPFS/z63-run"
	var c Config
	fs := flag.NewFlagSet("z63ui", flag.ContinueOnError)
	fs.StringVar(&c.QEMU, "qemu", "/Volumes/ExAPFS/qemu-z63/build/qemu-system-aarch64", "QEMU binary")
	fs.StringVar(&c.Dir, "dir", run, "QEMU's working directory")
	fs.StringVar(&c.Firmware, "fw", "/Volumes/ExAPFS/Z6_3_0200_modules", "firmware components")
	fs.StringVar(&c.Loader, "loader", "loader_0x40000000-0x40160000.bin", "loader band dump")
	fs.StringVar(&c.SNOR, "snor", run+"/claude/snor_user_st.bin", "SNOR_USER body")
	fs.StringVar(&c.NAND, "nand", run+"/nand-e.img", "NAND image (copied per run)")
	fs.StringVar(&c.Raw, "raw", "bars.raw", "Bayer file for z9-capture-raw")
	fs.StringVar(&c.Machine, "machine", "sio-sink=9:8:7", "more -machine options")
	fs.StringVar(&c.Plugin, "plugin", "/Volumes/ExAPFS/qemu-z63/z63-tools/plugins/libz63log.dylib", "z63log plugin, empty for none")
	fs.StringVar(&c.Gdb, "gdb", "tcp:127.0.0.1:41240", "gdbstub, empty for none")
	fs.StringVar(&c.Out, "out", run+"/claude/ui", "sockets, the live frame and runs/")
	fs.BoolVar(&c.NoStart, "no-start", false, "do not boot at launch")
	// macOS once passed -psn_ to apps opened from Finder; never fail on flags.
	if err := fs.Parse(args); err != nil {
		log.Printf("z63ui: %v", err)
	}
	c.Extra = fs.Args()
	if c.Plugin != "" {
		if _, err := os.Stat(c.Plugin); err != nil {
			log.Printf("z63ui: no z63log plugin at %s, firmware log off", c.Plugin)
			c.Plugin = ""
		}
	}
	return c
}

// Status is what both windows show at the top.
type Status struct {
	State   string   `json:"state"` // off, starting, running, paused, stopped, exited
	Detail  string   `json:"detail"`
	Run     string   `json:"run"`
	Started int64    `json:"started"` // unix ms
	Pid     int      `json:"pid"`
	Held    []string `json:"held"`
	Files   RunFiles `json:"files"`
	Cmdline []string `json:"cmdline"`
}

// RunFiles are the current run's files.
type RunFiles struct {
	Serial string `json:"serial"`
	Log    string `json:"log"`
	Z63log string `json:"z63log"`
	Live   string `json:"live"`
	Top    string `json:"top"`
	QMP    string `json:"qmp"`
	Gdb    string `json:"gdb"`
}

// LogBatch is a run of lines (or, for the serial console, raw text).
type LogBatch struct {
	Run     string   `json:"run"`
	Items   []string `json:"items"`
	Dropped int      `json:"dropped"` // items the page was too slow for
	Reset   bool     `json:"reset"`   // a new run starts with these items
}

// StatusChanged goes to every window when the machine's state moves.
var StatusChanged = mygo.NewEvent[Status]("status")

// Camera is the machine as both pages see it.
type Camera struct{ m *Machine }

// Status returns the machine's state.
func (c *Camera) Status() Status { return c.m.Status() }

// Config returns where everything is.
func (c *Camera) Config() Config { return c.m.cfg }

// Start boots a new run if none is running.
func (c *Camera) Start() error { return c.m.Start() }

// Stop ends the run: QMP quit, then a kill.
func (c *Camera) Stop() error { return c.m.Stop() }

// Restart stops the run and boots a new one.
func (c *Camera) Restart() error {
	if err := c.m.Stop(); err != nil {
		return err
	}
	return c.m.Start()
}

// Pause stops the vCPUs (QMP stop).
func (c *Camera) Pause() error { return c.m.Pause() }

// Resume continues them (QMP cont).
func (c *Camera) Resume() error { return c.m.Resume() }

// SetKeys holds exactly these buttons (mcu-keys names). seq orders calls,
// which arrive on goroutines of their own: an older one is dropped.
func (c *Camera) SetKeys(seq int, keys []string) error { return c.m.SetKeys(seq, keys) }

// SerialInput types text on the UART.
func (c *Camera) SerialInput(text string) error { return c.m.SerialInput(text) }

// Monitor runs a QEMU monitor (HMP) command.
func (c *Camera) Monitor(command string) (string, error) { return c.m.Monitor(command) }

// Touch sends contacts on the monitor to the machine.
func (c *Camera) Touch(points []TouchPoint) error { return c.m.Touch(points) }

// Screenshot saves the monitor's picture as a PNG next to the run's logs.
func (c *Camera) Screenshot() (string, error) { return c.m.Screenshot() }

// Tags counts stderr lines by their leading tag.
func (c *Camera) Tags() []TagCount { return c.m.stderr.Tags() }

// Serial streams the UART: the backlog, then text as it arrives.
func (c *Camera) Serial(ctx context.Context, ch *mygo.Channel[LogBatch]) error {
	return c.m.serial.Follow(ctx, 256<<10, ch.Send)
}

// Stderr streams QEMU's stderr, a line an item.
func (c *Camera) Stderr(ctx context.Context, ch *mygo.Channel[LogBatch]) error {
	return c.m.stderr.Follow(ctx, 4000, ch.Send)
}

// Firmware streams the z63log lines.
func (c *Camera) Firmware(ctx context.Context, ch *mygo.Channel[LogBatch]) error {
	return c.m.fwlog.Follow(ctx, 4000, ch.Send)
}

var (
	machine *Machine
	win     *mygo.Window
)

func main() {
	cfg := parseConfig(os.Args[1:])
	machine = NewMachine(cfg)
	mygo.Bind(&Camera{m: machine})
	frames := http.NewServeMux()
	frames.Handle("/top/", http.StripPrefix("/top", machine.top))
	frames.Handle("/", machine.live)
	if err := mygo.Protocol.Handle("frame", frames); err != nil {
		log.Fatal(err)
	}

	mygo.App.WhenReady(func() {
		if err := os.MkdirAll(filepath.Join(cfg.Out, "runs"), 0o755); err != nil {
			log.Fatal(err)
		}
		if err := listenControl(filepath.Join(cfg.Out, "ctl.sock")); err != nil {
			log.Fatal(err)
		}
		machine.OnStatus(func(s Status) { StatusChanged.Broadcast(s) })
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "Z 6III",
			URL:             "/",
			Width:           1680,
			Height:          1050,
			MinWidth:        1200,
			MinHeight:       760,
			StateKey:        "main",
			BackgroundColor: "#e9e8e4", // --page in src/style.css
		})
		win.OnClosed(func() { mygo.App.Quit() })
		if !cfg.NoStart {
			go func() {
				if err := machine.Start(); err != nil {
					log.Printf("z63ui: start: %v", err)
				}
			}()
		}
	})
	mygo.App.OnQuit(func() { machine.Stop() })
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
