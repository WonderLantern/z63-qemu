package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// The control socket: one command a line, one JSON reply a line. It is how
// a script (or a session over ssh, which cannot see the desktop) drives the
// app and looks at it:
//
//	status                        the machine's Status
//	start | stop | restart | pause | resume
//	keys menu:ok                  hold exactly these buttons ("keys" alone releases)
//	press <key> [secs]            hold one for secs (default 0.3), then release
//	serial <text>                 type on the UART (\n, \r and \t are unescaped)
//	hmp <command>                 a monitor command
//	shot                          the monitor's picture to runs/, as Screenshot
//	capture <path>                the window as a PNG (what is on screen)
//	eval <js>                     run JavaScript in the page, return its value
//
//	echo status | nc -U /Volumes/ExAPFS/z63-run/claude/ui/ctl.sock
func listenControl(path string) error {
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		return errors.New("another z63ui is running (" + path + " answers)")
	}
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveControl(c)
		}
	}()
	mygo.App.OnQuit(func() { ln.Close(); os.Remove(path) })
	return nil
}

func serveControl(c net.Conn) {
	defer c.Close()
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	enc := json.NewEncoder(c)
	for sc.Scan() {
		v, err := control(strings.TrimSpace(sc.Text()))
		if err != nil {
			enc.Encode(map[string]any{"error": err.Error()})
		} else {
			enc.Encode(map[string]any{"ok": v})
		}
	}
}

func window() (*mygo.Window, error) {
	if win == nil || win.IsDestroyed() {
		return nil, errors.New("the window is closed")
	}
	return win, nil
}

func control(line string) (any, error) {
	cmd, rest, _ := strings.Cut(line, " ")
	m := machine
	switch cmd {
	case "status":
		return m.Status(), nil
	case "start":
		return nil, m.Start()
	case "stop":
		return nil, m.Stop()
	case "restart":
		if err := m.Stop(); err != nil {
			return nil, err
		}
		return nil, m.Start()
	case "pause":
		return nil, m.Pause()
	case "resume":
		return nil, m.Resume()
	case "keys":
		var keys []string
		if rest != "" {
			keys = strings.Split(rest, ":")
		}
		return nil, m.SetKeys(0, keys)
	case "press":
		key, secs, _ := strings.Cut(rest, " ")
		d := 0.3
		if secs != "" {
			d, _ = strconv.ParseFloat(secs, 64)
		}
		if err := m.SetKeys(0, []string{key}); err != nil {
			return nil, err
		}
		time.Sleep(time.Duration(d * float64(time.Second)))
		return nil, m.SetKeys(0, nil)
	case "serial":
		r := strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t")
		return nil, m.SerialInput(r.Replace(rest))
	case "hmp":
		return m.Monitor(rest)
	case "shot":
		return m.Screenshot()
	case "capture":
		w, err := window()
		if err != nil {
			return nil, err
		}
		b, err := w.CapturePage()
		if err != nil {
			return nil, err
		}
		return rest, os.WriteFile(rest, b, 0o644)
	case "eval":
		w, err := window()
		if err != nil {
			return nil, err
		}
		return w.Page().Eval(rest)
	}
	return nil, fmt.Errorf("unknown command %q", cmd)
}
