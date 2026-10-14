package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Machine is the QEMU child and everything that follows it.
type Machine struct {
	cfg    Config
	serial *hub  // UART text, as it comes
	stderr *hub  // QEMU's stderr, a line an item
	fwlog  *hub  // z63log
	live   *live // the scanout
	top    *live // the control panel

	mu       sync.Mutex
	run      *run
	status   Status
	onStatus func(Status)
	keySeq   int
	shots    int
}

type run struct {
	name   string
	cmd    *exec.Cmd
	cancel context.CancelFunc
	files  RunFiles
	done   chan struct{} // closed when QEMU has exited

	mu       sync.Mutex
	qmp      *qmp
	uart     net.Conn
	stopping string // the last "-- stopping" line on stderr
	shutdown bool   // a SHUTDOWN event came before the STOP
}

func NewMachine(cfg Config) *Machine {
	return &Machine{
		cfg:    cfg,
		serial: newHub(4<<20, false, true),
		stderr: newHub(24<<20, true, false),
		fwlog:  newHub(16<<20, false, false),
		live:   newLive(),
		top:    newLive(),
		status: Status{State: "off"},
	}
}

func (m *Machine) OnStatus(fn func(Status)) {
	m.mu.Lock()
	m.onStatus = fn
	m.mu.Unlock()
}

func (m *Machine) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// setState moves the state of run r (ignored once r is not current).
func (m *Machine) setState(r *run, state, detail string) {
	m.mu.Lock()
	if r != nil && m.run != r {
		m.mu.Unlock()
		return
	}
	m.status.State, m.status.Detail = state, detail
	s, fn := m.status, m.onStatus
	m.mu.Unlock()
	if fn != nil {
		fn(s)
	}
}

func (m *Machine) current() *run {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.run
}

// Start boots a new run, as run2.sh does, with the UART, QMP, the live
// frame and z63log going to this process.
func (m *Machine) Start() error {
	m.mu.Lock()
	if m.run != nil {
		m.mu.Unlock()
		return errors.New("a run is going; stop it first")
	}
	c := m.cfg
	name := time.Now().Format("ui-0102-150405")
	runs := filepath.Join(c.Out, "runs")
	r := &run{name: name, done: make(chan struct{})}
	r.files = RunFiles{
		Serial: filepath.Join(runs, name+".serial"),
		Log:    filepath.Join(runs, name+".log"),
		Live:   filepath.Join(c.Out, "live-"+name+".bin"),
		Top:    filepath.Join(c.Out, "top-"+name+".bin"),
		QMP:    filepath.Join(c.Out, "qmp.sock"),
		Gdb:    c.Gdb,
	}
	if c.Plugin != "" {
		r.files.Z63log = filepath.Join(runs, name+".z63log")
	}
	m.run = r
	m.status = Status{State: "starting", Run: name, Started: time.Now().UnixMilli(), Files: r.files}
	m.mu.Unlock()

	fail := func(err error) error {
		m.mu.Lock()
		m.run = nil
		m.mu.Unlock()
		m.setState(nil, "off", err.Error())
		return err
	}
	if err := os.MkdirAll(runs, 0o755); err != nil {
		return fail(err)
	}
	// The firmware writes to its NAND: a fresh copy each run, never the image.
	nand := filepath.Join(c.Out, "nand-ui.img")
	if err := copyFile(c.NAND, nand); err != nil {
		return fail(fmt.Errorf("NAND copy: %w", err))
	}
	m.live.release()
	m.top.release()
	for _, pat := range []string{"live-*.bin", "top-*.bin"} {
		old, _ := filepath.Glob(filepath.Join(c.Out, pat))
		for _, f := range old {
			os.Remove(f)
		}
	}
	uartPath := filepath.Join(c.Out, "uart.sock")
	os.Remove(uartPath)
	ln, err := net.Listen("unix", uartPath)
	if err != nil {
		return fail(err)
	}
	os.Remove(r.files.QMP)

	mopt := []string{
		"nikon-z63",
		"loader=" + c.Loader,
		"snor-user=" + c.SNOR,
		"nand=" + nand,
		"mcu-image=" + filepath.Join(c.Firmware, "ex2010_0200a0.bi"),
		"z9-capture=on",
		"z9-capture-raw=" + c.Raw,
		"gfx-skip-unmodelled=on",
		"scanout-live=" + r.files.Live,
		"toplcd-live=" + r.files.Top,
	}
	if c.Machine != "" {
		mopt = append(mopt, c.Machine)
	}
	args := []string{
		"-name", "z63-ui",
		"-machine", strings.Join(mopt, ","),
		"-cpu", "cortex-a53", "-smp", "4,maxcpus=5",
		"-bios", filepath.Join(c.Firmware, "eg2010_0200i1.bi"),
		"-chardev", "socket,id=uart,path=" + uartPath + ",logfile=" + r.files.Serial,
		"-serial", "chardev:uart",
		"-monitor", "none", "-display", "none",
		"-qmp", "unix:" + r.files.QMP + ",server=on,wait=off",
		// A refusal pauses the machine on its last frame rather than
		// ending QEMU; the reason is on stderr.
		"-action", "shutdown=pause",
	}
	if c.Gdb != "" {
		args = append(args, "-gdb", c.Gdb)
	}
	if c.Plugin != "" {
		args = append(args, "-plugin", c.Plugin+",out="+r.files.Z63log)
	}
	args = append(args, c.Extra...)

	logf, err := os.Create(r.files.Log)
	if err != nil {
		ln.Close()
		return fail(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	cmd := exec.Command(c.QEMU, args...)
	cmd.Dir = c.Dir
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	r.cmd = cmd
	m.serial.begin(name)
	m.stderr.begin(name)
	m.fwlog.begin(name)
	m.mu.Lock()
	m.status.Cmdline = append([]string{c.QEMU}, args...)
	m.keySeq = 0
	m.mu.Unlock()
	if err := cmd.Start(); err != nil {
		cancel()
		ln.Close()
		logf.Close()
		return fail(err)
	}
	m.mu.Lock()
	m.status.Pid = cmd.Process.Pid
	m.mu.Unlock()
	m.setState(r, "starting", "")

	go m.readStderr(r, pr, logf)
	go m.acceptUART(r, ln)
	go m.connectQMP(ctx, r)
	if r.files.Z63log != "" {
		go tailFile(ctx, r.files.Z63log, m.fwlog.add)
	}
	m.live.follow(r.files.Live)
	m.top.follow(r.files.Top)
	go func() {
		err := cmd.Wait()
		pw.Close()
		cancel()
		ln.Close()
		close(r.done)
		detail := "exit 0"
		if err != nil {
			detail = err.Error()
		}
		r.mu.Lock()
		if r.stopping != "" {
			detail += " -- " + r.stopping
		}
		r.mu.Unlock()
		m.setState(r, "exited", detail)
		m.mu.Lock()
		if m.run == r {
			m.run = nil
			m.status.Pid = 0
		}
		m.mu.Unlock()
	}()
	return nil
}

func (m *Machine) readStderr(r *run, pr io.Reader, logf *os.File) {
	defer logf.Close()
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	w := bufio.NewWriterSize(logf, 256<<10)
	defer w.Flush()
	last := time.Now()
	for sc.Scan() {
		line := sc.Text()
		w.WriteString(line)
		w.WriteByte('\n')
		if time.Since(last) > time.Second {
			w.Flush()
			last = time.Now()
		}
		m.stderr.add(line)
		if strings.Contains(line, "-- stopping") || strings.Contains(line, "stopping the machine") {
			r.mu.Lock()
			r.stopping = strings.TrimPrefix(line, "qemu-system-aarch64: ")
			r.mu.Unlock()
		}
	}
}

func (m *Machine) acceptUART(r *run, ln net.Listener) {
	conn, err := ln.Accept()
	ln.Close()
	if err != nil {
		return
	}
	r.mu.Lock()
	r.uart = conn
	r.mu.Unlock()
	buf := make([]byte, 64<<10)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			m.serial.add(string(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}

func (m *Machine) connectQMP(ctx context.Context, r *run) {
	q, err := dialQMP(ctx, r.files.QMP, func(ev string, data json.RawMessage) { m.qmpEvent(r, ev, data) })
	if err != nil {
		return
	}
	r.mu.Lock()
	r.qmp = q
	r.mu.Unlock()
	go func() { <-ctx.Done(); q.close() }()
	if ret, err := q.cmd("query-status", nil); err == nil {
		var st struct {
			Running bool   `json:"running"`
			Status  string `json:"status"`
		}
		json.Unmarshal(ret, &st)
		if st.Running {
			m.setState(r, "running", "")
		} else {
			m.setState(r, "paused", st.Status)
		}
	}
}

func (m *Machine) qmpEvent(r *run, ev string, data json.RawMessage) {
	switch ev {
	case "SHUTDOWN":
		var d struct {
			Reason string `json:"reason"`
		}
		json.Unmarshal(data, &d)
		r.mu.Lock()
		r.shutdown = true
		r.mu.Unlock()
	case "STOP":
		r.mu.Lock()
		shut, why := r.shutdown, r.stopping
		r.mu.Unlock()
		if shut {
			m.setState(r, "stopped", why)
		} else {
			m.setState(r, "paused", "")
		}
	case "RESUME":
		r.mu.Lock()
		r.shutdown = false
		r.mu.Unlock()
		m.setState(r, "running", "")
	case "RESET":
		m.stderr.add("z63ui: QMP event RESET " + string(data))
	}
}

// Stop ends the run: QMP quit, and a kill if QEMU is still there in 3 s.
func (m *Machine) Stop() error {
	r := m.current()
	if r == nil {
		return nil
	}
	r.mu.Lock()
	q := r.qmp
	r.mu.Unlock()
	if q != nil {
		q.cmd("quit", nil)
	}
	select {
	case <-r.done:
	case <-time.After(3 * time.Second):
		r.cmd.Process.Signal(syscall.SIGKILL)
		<-r.done
	}
	return nil
}

func (m *Machine) qmpCmd(name string, args any) (json.RawMessage, error) {
	r := m.current()
	if r == nil {
		return nil, errors.New("the machine is off")
	}
	r.mu.Lock()
	q := r.qmp
	r.mu.Unlock()
	if q == nil {
		return nil, errors.New("QMP is not connected yet")
	}
	return q.cmd(name, args)
}

func (m *Machine) Pause() error {
	_, err := m.qmpCmd("stop", nil)
	return err
}

func (m *Machine) Resume() error {
	_, err := m.qmpCmd("cont", nil)
	return err
}

func (m *Machine) SetKeys(seq int, keys []string) error {
	m.mu.Lock()
	if seq != 0 && seq <= m.keySeq {
		m.mu.Unlock()
		return nil
	}
	if seq != 0 {
		m.keySeq = seq
	}
	m.mu.Unlock()
	_, err := m.qmpCmd("qom-set", map[string]any{"path": "/machine", "property": "mcu-keys", "value": strings.Join(keys, ":")})
	if err == nil {
		m.mu.Lock()
		m.status.Held = append([]string(nil), keys...)
		s, fn := m.status, m.onStatus
		m.mu.Unlock()
		if fn != nil {
			fn(s)
		}
	}
	return err
}

func (m *Machine) SerialInput(text string) error {
	r := m.current()
	if r == nil {
		return errors.New("the machine is off")
	}
	r.mu.Lock()
	c := r.uart
	r.mu.Unlock()
	if c == nil {
		return errors.New("the UART is not connected yet")
	}
	_, err := c.Write([]byte(text))
	return err
}

func (m *Machine) Monitor(command string) (string, error) {
	ret, err := m.qmpCmd("human-monitor-command", map[string]any{"command-line": command})
	if err != nil {
		return "", err
	}
	var s string
	json.Unmarshal(ret, &s)
	return s, nil
}

// TouchPoint is one contact on the monitor; X and Y run from 0 to 1 across
// the picture.
type TouchPoint struct {
	Type string  `json:"type"` // begin, update, end, cancel
	Slot int     `json:"slot"`
	ID   int     `json:"id"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

// Touch sends contacts as QEMU multi-touch input, the sequence QEMU's own
// windows send for a touch screen (ui/input.c qemu_input_queue_mtt and
// _mtt_abs): the contact's type, then for begin and update its position
// as two data events, 0..0x7fff across the picture. Nothing in the board
// takes multi-touch input yet; until a touch controller model registers
// for it, QEMU answers "Input handler not found for event type mtt".
func (m *Machine) Touch(points []TouchPoint) error {
	ev := func(typ string, p TouchPoint, axis string, v float64) map[string]any {
		return map[string]any{"type": "mtt", "data": map[string]any{
			"type": typ, "slot": p.Slot, "tracking-id": p.ID, "axis": axis,
			"value": int(min(max(v, 0), 1) * 0x7fff),
		}}
	}
	var evs []map[string]any
	for _, p := range points {
		evs = append(evs, ev(p.Type, p, "x", 0))
		if p.Type == "begin" || p.Type == "update" {
			evs = append(evs, ev("data", p, "x", p.X), ev("data", p, "y", p.Y))
		}
	}
	_, err := m.qmpCmd("input-send-event", map[string]any{"events": evs})
	return err
}

// Screenshot writes the last frame to runs/<run>-shot-<n>.png.
func (m *Machine) Screenshot() (string, error) {
	f := m.live.current()
	if f.w == 0 {
		return "", errors.New("no frame yet")
	}
	m.mu.Lock()
	m.shots++
	name := fmt.Sprintf("%s-shot-%d.png", m.status.Run, m.shots)
	m.mu.Unlock()
	path := filepath.Join(m.cfg.Out, "runs", name)
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer out.Close()
	return path, png.Encode(out, rgbaImage(f))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// tailFile sends the lines appended to path until ctx ends.
func tailFile(ctx context.Context, path string, line func(string)) {
	var f *os.File
	for f == nil {
		var err error
		if f, err = os.Open(path); err != nil {
			f = nil
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 256<<10)
	var part string
	for {
		s, err := r.ReadString('\n')
		if err == nil {
			line(strings.TrimSuffix(part+s, "\n"))
			part = ""
			continue
		}
		part += s
		select {
		case <-ctx.Done():
			// What QEMU wrote just before it ended.
			for {
				s, err := r.ReadString('\n')
				if s != "" {
					line(strings.TrimSuffix(part+s, "\n"))
					part = ""
				}
				if err != nil {
					return
				}
			}
		case <-time.After(150 * time.Millisecond):
		}
	}
}
