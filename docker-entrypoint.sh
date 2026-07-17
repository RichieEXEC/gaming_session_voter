#!/bin/sh
# Volume připojený zvenku (Coolify, bind mount, ručně vytvořený volume)
# přijde skoro vždycky jako root:root. Image sám o sobě vlastnictví
# neopraví: mount build-time chown přebije. Takže startujeme jako root,
# srovnáme vlastnictví a teprve pak spadneme na neprivilegovaného
# uživatele. Aplikace samotná pod rootem neběží.
set -e

DB_PATH="${DB_PATH:-/data/kdyhrajeme.db}"
DATA_DIR="$(dirname "$DB_PATH")"

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$DATA_DIR"
  chown -R app:app "$DATA_DIR"
  exec su-exec app "$@"
fi

# Když nás platforma pustí rovnou pod neprivilegovaným uživatelem,
# chown není čím udělat. Aspoň ať je z chyby poznat, co je špatně:
# SQLite v takovém případě hlásí "out of memory (14)", což nikoho
# nenavede.
if [ ! -w "$DATA_DIR" ]; then
  echo "kdy-hrajeme: do adresáře $DATA_DIR nemůže zapisovat uid $(id -u)." >&2
  echo "kdy-hrajeme: připoj volume zapisovatelný pro uid 10001, nebo nech kontejner nastartovat jako root." >&2
  exit 1
fi

exec "$@"
