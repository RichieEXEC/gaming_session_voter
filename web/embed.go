// Package web nese šablony a statické soubory zabalené v binárce,
// aby výsledný kontejner byl jeden soubor bez závislostí.
package web

import "embed"

//go:embed templates/*.html static/*
var Files embed.FS
