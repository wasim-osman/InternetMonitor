package install

import (
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUnit(t *testing.T) {
	u := Unit(":8080")
	for _, want := range []string{
		"ExecStart=/usr/local/bin/imonitord run --listen :8080 --state-dir /var/lib/imonitor",
		"DynamicUser=yes", "StateDirectory=imonitor", "AmbientCapabilities=CAP_NET_RAW", "Restart=always",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit lacks %q", want)
		}
	}
	if got := ParseInstalledListen(u); got != ":8080" {
		t.Fatalf("listen from unit = %q", got)
	}
}

// wellFormed checks that s parses as XML.
func wellFormed(t *testing.T, s string) {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(s))
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	for {
		_, err := d.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("not well-formed XML: %v\n%s", err, s)
		}
	}
}

func TestLaunchdPlist(t *testing.T) {
	p := LaunchdPlist(DarwinBinPath, "127.0.0.1:7900", UnixStateDir, DarwinLogPath)
	wellFormed(t, p)
	for _, want := range []string{
		"<string>com.wasim.imonitord</string>",
		"<string>/usr/local/bin/imonitord</string>\n\t\t<string>run</string>",
		"<key>RunAtLoad</key>\n\t<true/>", "<key>KeepAlive</key>\n\t<true/>",
		"<key>StandardOutPath</key>\n\t<string>/var/log/imonitord.log</string>",
		"<key>StandardErrorPath</key>\n\t<string>/var/log/imonitord.log</string>",
		"<string>--state-dir</string>\n\t\t<string>/var/lib/imonitor</string>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q", want)
		}
	}
	if got := ParseInstalledListen(p); got != "127.0.0.1:7900" {
		t.Fatalf("listen from plist = %q", got)
	}
	// Escaping.
	if q := LaunchdPlist("/opt/a&b/imonitord", ":1", "/x", "/y"); !strings.Contains(q, "/opt/a&amp;b/imonitord") {
		t.Fatal("plist values are not XML-escaped")
	}
	// On macOS, let plutil validate it for real.
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("plutil"); err == nil {
			f := filepath.Join(t.TempDir(), "test.plist")
			os.WriteFile(f, []byte(p), 0o644)
			if out, err := exec.Command("plutil", "-lint", f).CombinedOutput(); err != nil {
				t.Fatalf("plutil -lint: %v %s", err, out)
			}
		}
	}
}

func TestWinEscapeArg(t *testing.T) {
	cases := map[string]string{
		"":                               `""`,
		"run":                            "run",
		`C:\ProgramData\InternetMonitor`: `C:\ProgramData\InternetMonitor`,
		`C:\Program Files\x.exe`:         `"C:\Program Files\x.exe"`,
		`a"b`:                            `"a\"b"`,
		`C:\dir with space\`:             `"C:\dir with space\\"`,
		`a\"b c`:                         `"a\\\"b c"`,
	}
	for in, want := range cases {
		if got := WinEscapeArg(in); got != want {
			t.Errorf("WinEscapeArg(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestWindowsServiceCommandLines(t *testing.T) {
	exe := `C:\Program Files\InternetMonitor\imonitord.exe`
	state := `C:\ProgramData\InternetMonitor`
	bp := WinBinPath(exe, ":7878", state)
	wantBP := `"C:\Program Files\InternetMonitor\imonitord.exe" run --service --listen :7878 --state-dir C:\ProgramData\InternetMonitor`
	if bp != wantBP {
		t.Fatalf("binPath\n got %s\nwant %s", bp, wantBP)
	}
	sc := `C:\Windows\System32\sc.exe`
	got := WinScCreate(sc, bp, false)
	want := `C:\Windows\System32\sc.exe create imonitord binPath= "\"C:\Program Files\InternetMonitor\imonitord.exe\" run --service --listen :7878 --state-dir C:\ProgramData\InternetMonitor" start= auto DisplayName= "InternetMonitor agent"`
	if got != want {
		t.Fatalf("sc create\n got %s\nwant %s", got, want)
	}
	if !strings.HasPrefix(WinScCreate(sc, bp, true), sc+" config imonitord binPath= ") {
		t.Fatal("sc config")
	}
	fl := WinScFailure(sc)
	if fl[0] != sc+" failure imonitord reset= 86400 actions= restart/2000/restart/5000/restart/10000" ||
		fl[1] != sc+" failureflag imonitord 1" || !strings.HasPrefix(fl[2], sc+` description imonitord "InternetMonitor agent`) {
		t.Fatalf("sc failure: %q", fl)
	}
	// sc qc output carries the binPath; pair-code reads --listen from it.
	qc := "[SC] QueryServiceConfig SUCCESS\r\n\r\nSERVICE_NAME: imonitord\r\n        TYPE               : 10  WIN32_OWN_PROCESS\r\n" +
		"        START_TYPE         : 2   AUTO_START\r\n        BINARY_PATH_NAME   : \"C:\\Program Files\\InternetMonitor\\imonitord.exe\" run --service --listen 0.0.0.0:7900 --state-dir C:\\ProgramData\\InternetMonitor\r\n"
	if l := ParseInstalledListen(qc); l != "0.0.0.0:7900" {
		t.Fatalf("listen from sc qc = %q", l)
	}

	netsh := `C:\Windows\System32\netsh.exe`
	add := WinFirewallAdd(netsh, exe, 7878)
	wantAdd := `C:\Windows\System32\netsh.exe advfirewall firewall add rule name="InternetMonitor agent (imonitord)" dir=in action=allow protocol=TCP localport=7878 program="C:\Program Files\InternetMonitor\imonitord.exe" profile=any enable=yes`
	if add != wantAdd {
		t.Fatalf("netsh add\n got %s\nwant %s", add, wantAdd)
	}
	if del := WinFirewallDelete(netsh); del != `C:\Windows\System32\netsh.exe advfirewall firewall delete rule name="InternetMonitor agent (imonitord)"` {
		t.Fatalf("netsh delete %s", del)
	}
}

func TestParseScState(t *testing.T) {
	out := "\r\nSERVICE_NAME: imonitord \r\n        TYPE               : 10  WIN32_OWN_PROCESS  \r\n        STATE              : 4  RUNNING \r\n                                (STOPPABLE, NOT_PAUSABLE, ACCEPTS_SHUTDOWN)\r\n        WIN32_EXIT_CODE    : 0  (0x0)\r\n"
	if st, ok := ParseScState(out); !ok || st != 4 {
		t.Fatalf("running: %d %v", st, ok)
	}
	if st, ok := ParseScState("        STATE              : 1  STOPPED \r\n"); !ok || st != 1 {
		t.Fatalf("stopped: %d %v", st, ok)
	}
	// Localized label, constant state name.
	if st, ok := ParseScState("        STATUS             : 2  START_PENDING\r\n"); !ok || st != 2 {
		t.Fatalf("pending: %d %v", st, ok)
	}
	if _, ok := ParseScState("[SC] EnumQueryServicesStatus:OpenService FAILED 1060:"); ok {
		t.Fatal("missing service must not parse")
	}
}

func TestWinTaskXML(t *testing.T) {
	exe := `C:\Program Files\InternetMonitor\imonitord.exe`
	args := WinServiceArgs(":7878", `C:\ProgramData\Internet Monitor`, false)
	x := WinTaskXML(exe, args)
	wellFormed(t, x)
	for _, want := range []string{
		"<UserId>S-1-5-18</UserId>", "<BootTrigger>", "<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
		"<Command>C:\\Program Files\\InternetMonitor\\imonitord.exe</Command>",
		`<Arguments>run --self-restart --log-file &#34;C:\ProgramData\Internet Monitor\imonitord.log&#34; --listen :7878 --state-dir &#34;C:\ProgramData\Internet Monitor&#34;</Arguments>`,
	} {
		if !strings.Contains(x, want) {
			t.Errorf("task XML lacks %q", want)
		}
	}
	if l := ParseInstalledListen(x); l != ":7878" {
		t.Fatalf("listen from task XML = %q", l)
	}
}

func TestChooseStateDir(t *testing.T) {
	yes := func(string) bool { return true }
	no := func(string) bool { return false }
	if ChooseStateDir("/var/lib/imonitor", "/home/u/.local/state/imonitor", yes) != "/var/lib/imonitor" {
		t.Fatal("system dir should win when writable")
	}
	if ChooseStateDir("/var/lib/imonitor", "/home/u/.local/state/imonitor", no) != "/home/u/.local/state/imonitor" {
		t.Fatal("user dir fallback")
	}
	if !DirWritable(t.TempDir()) {
		t.Fatal("temp dir should be writable")
	}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 && DirWritable("/var/lib/imonitor-test-not-writable") {
		t.Fatal("/var/lib should not be writable for a normal user")
	}
	if UserStateDir() == "" || SystemStateDir() == "" {
		t.Fatal("empty default dirs")
	}
}

func TestListenPort(t *testing.T) {
	if p, err := ListenPort(":7878"); err != nil || p != 7878 {
		t.Fatal(p, err)
	}
	if p, err := ListenPort("[::]:80"); err != nil || p != 80 {
		t.Fatal(p, err)
	}
	if _, err := ListenPort("7878"); err == nil {
		t.Fatal("want error")
	}
}
