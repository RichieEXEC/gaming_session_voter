# Kdy hrajeme

Date polling for a group that plays games together. Someone creates a poll,
posts the link, everyone marks the dates they can make. No accounts: voters
identify with a nickname they type in.

Czech by default, English available. Dark by default, light theme included.

## How it reads

Availability is encoded as density of yellow rather than a red/amber/green
traffic light: a solid cell is yes, a hatched cell is maybe, an empty cell is
no. The date that suits most people is the row that visibly glows, and the
tally bar and `NEJLEPŠÍ` chip confirm what your eye already caught.

Scoring is `yes = 1`, `maybe = 0.5`, `no = 0`. Ties go to the earlier date.

## Running it

```sh
docker compose up --build
# http://localhost:8080
```

Or without compose:

```sh
docker build -t kdy-hrajeme .
docker run -p 8080:8080 -v kdyhrajeme-data:/data kdy-hrajeme
```

## Deploying on Coolify

1. New Resource, then point it at this git repository.
2. Build pack: **Dockerfile** (Coolify detects it; compose also works).
3. Add a **persistent volume** mounted at `/data`. Without it the database is
   wiped on every redeploy. Leave *Source Path* empty so you get a Docker
   named volume; filling it in makes a bind mount to that host path, which
   works but points a host directory at the app for no benefit.
4. Port `8080` is exposed. Coolify terminates TLS in front of the app, which
   the app accounts for via `X-Forwarded-Proto` when setting secure cookies.
5. Health check endpoint is `/healthz`.

### If it won't start: "out of memory (14)"

```
ERROR msg="open store" err="ping sqlite: unable to open database file: out of memory (14)"
```

This is not memory. SQLite code 14 is `SQLITE_CANTOPEN`, and the pure-Go
driver renders it with that unhelpful text: the app cannot create the
database file because the mounted `/data` is not writable by it.

A mount always covers whatever the image did to that path, so a build-time
`chown` cannot fix it. The container therefore starts as root, has the
entrypoint `chown` the data directory, and drops to the `app` user via
`su-exec` before running the binary. The app process itself never runs as
root. If your platform forces a non-root user so the entrypoint cannot
chown, the app fails fast with a message naming the directory and uid
instead of the SQLite text.

### Environment

| Variable  | Default                | Notes                                       |
|-----------|------------------------|---------------------------------------------|
| `PORT`    | `8080`                 | Listen port.                                |
| `DB_PATH` | `/data/kdyhrajeme.db`  | SQLite file. Put it on the volume.          |
| `TZ`      | `UTC`                  | Decides which day counts as "today". Set it. |

Set `TZ` (for example `Europe/Prague`). It only affects the date prefilled
into a new poll, and only near midnight, but on a UTC container that prefill
is a day behind for anyone east of Greenwich. The browser corrects the first
date to its own today while the field is untouched, so this mostly matters
with JavaScript off.

There is no secret to configure. The key used to sign vote cookies is
generated on first run and kept in the database, so restarts don't sign
people out of their own votes.

## Design notes

**No accounts, by design.** A poll lives at an unguessable URL. Identity is a
self-declared nickname. This guards against the mundane failure (two people
picking the same nickname) rather than against impersonation, which is the
right trade for a small group of colleagues who already trust each other.

**Editing your vote.** On save, the browser gets a cookie holding the vote id
and an HMAC of it, scoped to that poll's path. On return, the app prefills
your column and updates in place rather than adding a second row. Clearing
cookies means you'd vote again under a new nickname; that is an acceptable
failure for this audience.

**Czech plurals are a real feature, not a formatting detail.** Czech branches
on 1 / 2-4 / 5+ (`1 termín`, `3 termíny`, `5 termínů`), so plural forms live
in the message catalog with `.one` / `.few` / `.many` suffixes rather than in
string concatenation. `internal/i18n` owns the rule and the tests pin all
three branches. Adding a language means adding a catalog and, if its plural
rule differs, a branch in `pluralSuffix`.

Two Czech grammar traps worth knowing before you touch the copy:

- **Vocative.** "Thanks, Richard" is `díky, Richarde` in correct Czech, and
  you cannot decline an arbitrary nickname. The confirmation is therefore
  `Uloženo, díky!` with no name in it.
- **Gender.** "X already voted" is `hlasoval` or `hlasovala` depending on the
  person, which the app never knows. The warning is phrased
  `Tato přezdívka už hlasovala` so the verb agrees with *přezdívka*
  (feminine) instead of with the person.

**No long dashes.** Em dashes are essentially unused in Czech typography and
read as foreign. Use a colon or comma. The en dash in `18:30–22:00` is a
numeric range and is correct.

**Progressive enhancement.** The board and results are server-rendered and
readable without JavaScript. Cycling cells needs JS; there is a `<noscript>`
note saying so.

## Layout

```
main.go                  wiring, config, graceful shutdown
internal/store/          SQLite: schema, queries, cookie secret
internal/i18n/           catalogs, plural rules, date names
internal/app/            routes, handlers, board/scoring
web/templates/           html/template, one set per page
web/static/              css, js, favicon (embedded in the binary)
```

Templates and assets are embedded via `embed.FS`, so the container is one
static binary plus a data volume.

## Tests

```sh
go test ./...
```

Covers the Czech plural boundaries, catalog parity between languages, scoring,
and the case where someone editing their vote must not appear in the grid
twice while still counting toward the score.

## License

MIT. See [LICENSE](LICENSE).
