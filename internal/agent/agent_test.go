package agent

import "testing"

func TestParseAptList(t *testing.T) {
	out := `WARNING: apt does not have a stable CLI interface. Use with caution in scripts.

Listing...
bash/jammy-updates,jammy-security 5.1-6ubuntu1.1 amd64 [upgradable from: 5.1-6ubuntu1]
curl/jammy-updates 7.81.0-1ubuntu1.18 amd64 [upgradable from: 7.81.0-1ubuntu1.16]
openssl/stable-security 3.0.15-1~deb12u1 amd64 [upgradable from: 3.0.14-1~deb12u2]
`
	pkgs := parseAptList(out)
	if len(pkgs) != 3 {
		t.Fatalf("got %d packages, want 3: %+v", len(pkgs), pkgs)
	}
	if pkgs[0].Name != "bash" || pkgs[0].Version != "5.1-6ubuntu1.1" || !pkgs[0].Security {
		t.Errorf("bash parsed wrong: %+v", pkgs[0])
	}
	if pkgs[1].Security {
		t.Errorf("curl should not be security: %+v", pkgs[1])
	}
	if !pkgs[2].Security {
		t.Errorf("debian security source not detected: %+v", pkgs[2])
	}
}

func TestParseAptCheck(t *testing.T) {
	total, sec, ok := parseAptCheck("12;3\n")
	if !ok || total != 12 || sec != 3 {
		t.Fatalf("got %d %d %v", total, sec, ok)
	}
	if _, _, ok := parseAptCheck("garbage"); ok {
		t.Fatal("garbage parsed as ok")
	}
}

func TestRealFS(t *testing.T) {
	cases := []struct {
		fstype, mount string
		want          bool
	}{
		{"ext4", "/", true},
		{"xfs", "/data", true},
		{"vfat", "/boot/efi", true},
		{"tmpfs", "/run", false},
		{"overlay", "/var/lib/docker/overlay2/x/merged", false},
		{"squashfs", "/snap/core/1", false},
		{"ext4", "/snap/foo", false},
	}
	for _, c := range cases {
		if got := realFS(c.fstype, c.mount); got != c.want {
			t.Errorf("realFS(%q, %q) = %v, want %v", c.fstype, c.mount, got, c.want)
		}
	}
}

func TestParseOSRelease(t *testing.T) {
	s := "NAME=\"Ubuntu\"\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nID=ubuntu\n"
	if got := parseOSRelease(s); got != "Ubuntu 24.04.1 LTS" {
		t.Fatalf("got %q", got)
	}
}
