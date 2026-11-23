package sysinfo

import (
	"strconv"
	"strings"
)

// FormatWindowsVersion builds e.g. "Windows 11 Pro 23H2 (10.0.22631.4460)"
// from the CurrentVersion registry values. ProductName still says
// "Windows 10" on Windows 11, so builds >= 22000 are relabelled.
func FormatWindowsVersion(product, display, major, minor, build, ubr string) string {
	if b, err := strconv.Atoi(build); err == nil && b >= 22000 && strings.HasPrefix(product, "Windows 10") {
		product = "Windows 11" + strings.TrimPrefix(product, "Windows 10")
	}
	ver := ""
	if major != "" && build != "" {
		if minor == "" {
			minor = "0"
		}
		ver = major + "." + minor + "." + build
		if ubr != "" {
			ver += "." + ubr
		}
	} else if build != "" {
		ver = build
	}
	name := strings.TrimSpace(product + " " + display)
	switch {
	case name == "":
		return ver
	case ver == "":
		return name
	}
	return name + " (" + ver + ")"
}
