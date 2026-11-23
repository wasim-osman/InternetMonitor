package main

import (
	"path/filepath"
	"testing"

	"imonitord/internal/install"
)

func TestParseRunArgs(t *testing.T) {
	o, err := parseRunArgs(nil, "linux")
	if err != nil || o.Listen != ":7878" || o.StateDir != "" || o.Service || o.SelfRestart {
		t.Fatalf("defaults: %+v %v", o, err)
	}
	if _, err := parseRunArgs([]string{"--service"}, "linux"); err == nil {
		t.Fatal("--service must be rejected outside Windows")
	}
	if _, err := parseRunArgs([]string{"--listen", "7878"}, "linux"); err == nil {
		t.Fatal("bad listen must be rejected")
	}
	if _, err := parseRunArgs([]string{"extra"}, "linux"); err == nil {
		t.Fatal("stray argument must be rejected")
	}

	// What `imonitord install` registers on Windows must parse back exactly.
	state := `C:\ProgramData\InternetMonitor`
	args := install.WinServiceArgs(":7900", state, true)
	if args[0] != "run" {
		t.Fatalf("args %q", args)
	}
	o, err = parseRunArgs(args[1:], "windows")
	if err != nil || !o.Service || !o.SelfRestart || o.Listen != ":7900" || o.StateDir != state ||
		o.LogFile != filepath.Join(state, "imonitord.log") {
		t.Fatalf("service args: %+v %v", o, err)
	}
	// Scheduled-task fallback: no SCM handshake, but in-process restarts.
	args = install.WinServiceArgs(":7878", state, false)
	o, err = parseRunArgs(args[1:], "windows")
	if err != nil || o.Service || !o.SelfRestart || o.LogFile == "" || o.StateDir != state {
		t.Fatalf("task args: %+v %v", o, err)
	}
}
