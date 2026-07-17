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

// Přepnutí jazyka musí vracet tam, odkud se kliklo. Odkaz bez next
// spadne na domovskou stránku, což lidi vyhazovalo z hlasování.
func TestLangLinkCarriesNext(t *testing.T) {
	b, err := fs.ReadFile(web.Files, "templates/layout.html")
	if err != nil {
		t.Fatalf("read layout: %v", err)
	}
	if !bytes.Contains(b, []byte(`?next={{ .Path }}`)) {
		t.Error("odkaz na přepnutí jazyka neposílá next, po přepnutí by člověk skončil na /")
	}
}

func TestSafeNext(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/p/abc123", "/p/abc123"},
		{"/", "/"},
		{"", "/"},
		// Cizí server v jakémkoliv převleku končí na domovské stránce.
		{"//evil.example", "/"},
		{"/\\evil.example", "/"},
		{"https://evil.example/x", "/"},
		{"http://evil.example", "/"},
		{"javascript:alert(1)", "/"},
		{"mailto:a@b.c", "/"},
		{"evil.example", "/"},
		// Query se zahazuje, aby se nedala protáhnout stará hláška.
		{"/p/abc?flash=saved", "/p/abc"},
	}
	for _, c := range cases {
		if got := safeNext(c.in); got != c.want {
			t.Errorf("safeNext(%q) = %q, chci %q", c.in, got, c.want)
		}
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
