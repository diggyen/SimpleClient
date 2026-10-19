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
