package sysinfo

import "testing"

func TestFormatWindowsVersion(t *testing.T) {
	cases := []struct{ product, display, major, minor, build, ubr, want string }{
		{"Windows 10 Pro", "23H2", "10", "0", "22631", "4460", "Windows 11 Pro 23H2 (10.0.22631.4460)"},
		{"Windows 10 Enterprise", "22H2", "10", "0", "19045", "5131", "Windows 10 Enterprise 22H2 (10.0.19045.5131)"},
		{"Windows Server 2019 Datacenter", "", "10", "0", "17763", "", "Windows Server 2019 Datacenter (10.0.17763)"},
		{"", "", "", "", "", "", ""},
		{"", "", "", "", "9600", "", "9600"},
	}
	for _, c := range cases {
		if got := FormatWindowsVersion(c.product, c.display, c.major, c.minor, c.build, c.ubr); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}
