package bootenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// grubEditenvOutput is what `grub-editenv grubenv create` followed by
// `set sc_boot=A sc_kpending=1` writes.
func grubEditenvOutput() []byte {
	s := header + "sc_boot=A\nsc_kpending=1\n"
	return []byte(s + strings.Repeat("#", 1024-len(s)))
}

func TestParseReadsGRUBsFile(t *testing.T) {
	env, err := Parse(grubEditenvOutput())
	if err != nil {
		t.Fatal(err)
	}
	if env["sc_boot"] != "A" || env["sc_kpending"] != "1" || len(env) != 2 {
		t.Fatalf("got %v", env)
	}
}

func TestParseRejectsOtherFiles(t *testing.T) {
	if _, err := Parse([]byte("sc_boot=A\n")); err == nil {
		t.Fatal("a file without the GRUB header was accepted")
	}
}

func TestEncodeKeepsTheSize(t *testing.T) {
	b, err := Env{"sc_kernel": "lts", "sc_boot": "B"}.Encode(1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 1024 {
		t.Fatalf("size %d, want 1024", len(b))
	}
	if !strings.HasPrefix(string(b), header+"sc_boot=B\nsc_kernel=lts\n#") {
		t.Fatalf("unexpected layout: %q", b[:64])
	}
}

func TestEncodeRefusesWhatDoesNotFit(t *testing.T) {
	if _, err := (Env{"k": strings.Repeat("v", 2000)}).Encode(1024); err == nil {
		t.Fatal("an oversized environment was encoded")
	}
	if _, err := (Env{"k": "a\nb"}).Encode(1024); err == nil {
		t.Fatal("a value with a newline was encoded")
	}
}

func TestUpdateRewritesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grubenv")
	if err := os.WriteFile(path, grubEditenvOutput(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Update(path, map[string]string{"sc_kpending": "0", "sc_kernel": "new"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if len(b) != 1024 {
		t.Fatalf("size changed to %d", len(b))
	}
	env, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if env["sc_boot"] != "A" || env["sc_kpending"] != "0" || env["sc_kernel"] != "new" {
		t.Fatalf("got %v", env)
	}
}

func TestCurrentReadsTheCommandLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmdline")
	os.WriteFile(path, []byte("quiet lang=tr sc.slot=B sc.kernel=lts\n"), 0o644)
	old := cmdlinePath
	cmdlinePath = path
	defer func() { cmdlinePath = old }()

	b := Current()
	if b.Slot != "B" || b.Kernel != "lts" || !b.Installed() {
		t.Fatalf("got %+v", b)
	}

	os.WriteFile(path, []byte("quiet lang=tr sc.kernel=new\n"), 0o644)
	if b := Current(); b.Installed() || b.Kernel != "new" {
		t.Fatalf("live boot read as %+v", b)
	}
}

// fakePCI lays out a /sys/bus/pci/devices with the given devices, each
// "class vendor device".
func fakePCI(t *testing.T, devs ...string) {
	t.Helper()
	dir := t.TempDir()
	for i, d := range devs {
		f := strings.Fields(d)
		p := filepath.Join(dir, "dev"+string(rune('0'+i)))
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, "class"), []byte(f[0]+"\n"), 0o644)
		os.WriteFile(filepath.Join(p, "vendor"), []byte(f[1]+"\n"), 0o644)
		os.WriteFile(filepath.Join(p, "device"), []byte(f[2]+"\n"), 0o644)
	}
	old := pciPath
	pciPath = dir
	t.Cleanup(func() { pciPath = old })
}

func TestGPUsNamesTheDisplayAdapters(t *testing.T) {
	fakePCI(t, "0x060000 0x8086 0x4660", "0x030000 0x8086 0xa7a0",
		"0x020000 0x10ec 0x8168", "0x030200 0x10de 0x2860")
	if got := GPUs(); got != "10de:2860+8086:a7a0" {
		t.Fatalf("got %q", got)
	}
	fakePCI(t, "0x020000 0x10ec 0x8168")
	if got := GPUs(); got != "" {
		t.Fatalf("no display adapter, got %q", got)
	}
}

func TestSafeGraphics(t *testing.T) {
	for _, c := range []struct {
		list, gpus string
		safe       bool
	}{
		{"", "8086:46a6", false},
		{"once", "8086:46a6", true},
		{"10de:2860", "8086:46a6", false},
		{",1002:15bf,8086:46a6", "8086:46a6", true},
		{"8086:46a6+10de:2860", "8086:46a6", false},
		{",", "", false},
	} {
		if got := (Boot{GfxSafe: c.list}).SafeGraphics(c.gpus); got != c.safe {
			t.Errorf("list %q, gpus %q: safe %v, want %v", c.list, c.gpus, got, c.safe)
		}
	}
}

func TestCompactGPUs(t *testing.T) {
	if got := compactGPUs(",8086:46a6,,1002:15bf,8086:46a6"); got != "8086:46a6,1002:15bf" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat(",a", 3) + ",b,c,d,e,f,g"
	if got := compactGPUs(long); got != "b,c,d,e,f,g" {
		t.Fatalf("got %q", got)
	}
}

func TestModifyLeavesAnUnchangedFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grubenv")
	// Not this package's layout: a rewrite would reorder it.
	s := header + "sc_kpending=1\nsc_boot=A\n"
	os.WriteFile(path, []byte(s+strings.Repeat("#", 1024-len(s))), 0o644)
	if err := Modify(path, func(e Env) { e["sc_boot"] = "A" }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.HasPrefix(string(b), s) {
		t.Fatalf("rewritten: %q", b[:64])
	}
}

func TestCurrentReadsSafeGraphics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmdline")
	os.WriteFile(path, []byte("quiet sc.kernel=new sc.gfxsafe=,8086:46a6 sc.gfxsafe=once\n"), 0o644)
	old := cmdlinePath
	cmdlinePath = path
	defer func() { cmdlinePath = old }()
	if b := Current(); b.GfxSafe != "once" {
		t.Fatalf("got %+v", b)
	}
}
