package app

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"

	"github.com/RichieEXEC/gaming_session_voter/web"
)

func TestAssetURLsCarryContentHash(t *testing.T) {
	s := &Server{}
	if err := s.hashAssets(); err != nil {
		t.Fatalf("hashAssets: %v", err)
	}

	asset, ok := s.funcMap()["asset"].(func(string) string)
	if !ok {
		t.Fatal("funcMap nemá asset se správnou signaturou")
	}

	for _, name := range []string{"app.js", "app.css", "favicon.svg"} {
		got := asset(name)
		if !strings.HasPrefix(got, "/static/"+name+"?v=") {
			t.Errorf("asset(%q) = %q, chci /static/%s?v=<otisk>", name, got, name)
		}
	}
}

// Otisk se musí měnit s obsahem, jinak by nasazení nové verze prohlížeč
// přehlédl. Různé soubory tedy nesmí mít stejný otisk.
func TestAssetHashesDifferPerFile(t *testing.T) {
	s := &Server{}
	if err := s.hashAssets(); err != nil {
		t.Fatalf("hashAssets: %v", err)
	}
	seen := map[string]string{}
	for name, ver := range s.assetVer {
		if ver == "" {
			t.Errorf("%s má prázdný otisk", name)
		}
		if other, dup := seen[ver]; dup {
			t.Errorf("%s a %s mají stejný otisk %s", name, other, ver)
		}
		seen[ver] = name
	}
}

// Šablona nesmí na statický soubor odkazovat napřímo. Tím by se obešlo
// verzování a v prohlížeči by po nasazení zůstal viset starý app.js,
// zatímco HTML by už bylo nové. Přesně tohle se jednou stalo.
func TestTemplatesNeverHardcodeStaticPaths(t *testing.T) {
	entries, err := fs.ReadDir(web.Files, "templates")
	if err != nil {
		t.Fatalf("read templates: %v", err)
	}
	for _, e := range entries {
		b, err := fs.ReadFile(web.Files, "templates/"+e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if bytes.Contains(b, []byte(`"/static/`)) {
			t.Errorf("%s odkazuje na /static/ napřímo, použij {{ asset \"jmeno\" }}", e.Name())
		}
	}
}
