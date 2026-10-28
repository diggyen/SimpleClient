package bootenv

import (
	"os"
	"path/filepath"
	"strconv"
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
	os.WriteFile(path, []byte("quiet lang=tr sc.slot=B sc.kernel=lts sc.esp=1A2B-3C4D sc.data=0f6e\n"), 0o644)
	old := cmdlinePath
	cmdlinePath = path
	defer func() { cmdlinePath = old }()

	b := Current()
	if b.Slot != "B" || b.Kernel != "lts" || b.ESP != "1A2B-3C4D" || !b.Installed() || b.Other() != "A" {
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

// Only a boot left on the firmware framebuffer for these adapters' sake is one
// to restart out of; one the boot menu asked for stays as it is.
func TestLeftSafe(t *testing.T) {
	for _, c := range []struct {
		list, gpus string
		left       bool
	}{
		{"", "8086:5912", false},
		{",8086:5912", "8086:5912", true},
		{"10de:2860,8086:5912", "8086:5912", true},
		{"10de:2860", "8086:5912", false},
		{"once", "8086:5912", false},
		{",8086:5912,once", "8086:5912", false},
		{",8086:5912", "", false},
	} {
		if got := (Boot{GfxSafe: c.list}).LeftSafe(c.gpus); got != c.left {
			t.Errorf("list %q, gpus %q: %v, want %v", c.list, c.gpus, got, c.left)
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

func TestNewLoader(t *testing.T) {
	dir := t.TempDir()
	defer func(p string) { loaderPath = p }(loaderPath)
	loaderPath = filepath.Join(dir, "grub-disk.cfg")
	esp := filepath.Join(dir, "grub.cfg")
	if got := newLoader(esp); got != nil {
		t.Fatalf("a release without a script: got %q", got)
	}
	os.WriteFile(loaderPath, []byte("new rules\n"), 0o644)
	os.WriteFile(esp, []byte("old rules\n"), 0o644)
	got := newLoader(esp)
	if string(got) != "new rules\n" {
		t.Fatalf("got %q", got)
	}
	if err := replaceFile(esp, got); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(esp); string(b) != "new rules\n" {
		t.Fatalf("replaced with %q", b)
	}
	if _, err := os.Stat(esp + ".new"); !os.IsNotExist(err) {
		t.Fatalf("left the temporary file behind: %v", err)
	}
	if got := newLoader(esp); got != nil {
		t.Fatalf("the same script: got %q", got)
	}
}

// A boot left on the firmware framebuffer counts towards giving the driver
// another try: the tenth gives it one, and only one.
func TestMarkGoodRetriesSafeGraphics(t *testing.T) {
	const gpu = "8086:5912"
	b := Boot{Slot: "A", Kernel: "new", GfxSafe: ",10de:2860," + gpu}
	env := Env{"sc_gfxsafe": ",10de:2860," + gpu, "sc_kpending": "1"}
	for i := 1; i < safeRetryEvery; i++ {
		if markGood(env, b, gpu, false) {
			t.Fatalf("boot %d: asked for a restart", i)
		}
		if env["sc_gfxsafe"] != "10de:2860,"+gpu || env["sc_gfxsafeboots"] != strconv.Itoa(i) {
			t.Fatalf("boot %d: %v", i, env)
		}
	}
	if markGood(env, b, gpu, false) {
		t.Fatal("the retry asked for a restart; it waits for the next boot")
	}
	if env["sc_gfxsafe"] != "10de:2860" || env["sc_gfxfail"] != gpu {
		t.Fatalf("after %d boots: %v, want %s off the list and counted as failed once", safeRetryEvery, env, gpu)
	}
	if _, ok := env["sc_gfxsafeboots"]; ok {
		t.Fatalf("the count is left: %v", env)
	}
}

// Boots in safe graphics from the boot menu, or on another machine's GPU, or
// with no GPU, count nothing.
func TestMarkGoodLeavesOtherBootsAlone(t *testing.T) {
	for _, c := range []struct{ list, gpus string }{
		{"once", "8086:5912"},
		{",8086:5912,once", "8086:5912"},
		{"10de:2860", "8086:5912"},
		{",8086:5912", ""},
	} {
		env := Env{"sc_gfxsafe": c.list}
		markGood(env, Boot{GfxSafe: c.list}, c.gpus, false)
		if _, ok := env["sc_gfxsafeboots"]; ok || env["sc_gfxfail"] != "" {
			t.Errorf("list %q gpus %q: %v", c.list, c.gpus, env)
		}
	}
}

// New boot rules clear the list, the count with it, and restart only a boot
// that is in safe graphics for its GPU's sake.
func TestMarkGoodNewLoader(t *testing.T) {
	const gpu = "8086:5912"
	env := Env{"sc_gfxsafe": "," + gpu, "sc_gfxsafeboots": "4", "sc_gfxfail": "x"}
	if !markGood(env, Boot{Slot: "B", GfxSafe: "," + gpu}, gpu, true) {
		t.Fatal("no restart")
	}
	if env["sc_gfxsafe"] != "" || env["sc_gfxfail"] != "" || env["sc_boot"] != "B" {
		t.Fatalf("%v", env)
	}
	if _, ok := env["sc_gfxsafeboots"]; ok {
		t.Fatalf("the count is left: %v", env)
	}
	if markGood(Env{}, Boot{Slot: "B"}, gpu, true) {
		t.Fatal("a restart for a boot on its driver")
	}
}
