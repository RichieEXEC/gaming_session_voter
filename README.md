# Kdy hrajeme

Session planning for a group that plays games together. Someone creates a
**session**, posts the link, and everyone answers two questions at once on the
same page: **when** can you play (dates), and **what** do you want to play
(games). No accounts: voters identify with a nickname they type in.

Czech by default, English available. Dark by default, light theme included.

## How it reads

Each session has two boards, `KDY` (when) and `CO` (what), with one shared row
of voters. Availability and interest are both encoded as density of yellow
rather than a red/amber/green traffic light: a solid cell is yes, a hatched
cell is maybe, an empty cell is no. The row that suits most people is the one
that visibly glows, confirmed by the tally bar and `NEJLEPŠÍ` chip.

Scoring is `yes = 1`, `maybe = 0.5`, `no = 0`. Ties go to the earlier option.

**The two boards talk to each other.** The game board knows how many people
the leading date can gather, and flags a game whose max-player count can't fit
them (a `max 4` chip when six are coming). Nothing is settled while voting is
open, so this tracks the *provisionally* leading date and says `zatím` ("for
now"). If no date has votes yet, the check is off rather than guessing against
zero.

Games come from [IGDB](https://www.igdb.com/): anyone on the session can search
and add one. What's stored is a snapshot (name, year, genre, max players,
cover) taken when the game is added, so the board renders with no external
calls and keeps working even if IGDB is down. Removing a game that already has
votes asks for confirmation first.

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

| Variable             | Default               | Notes                                        |
|----------------------|-----------------------|----------------------------------------------|
| `PORT`               | `8080`                | Listen port.                                 |
| `DB_PATH`            | `/data/kdyhrajeme.db` | SQLite file. Put it on the volume.           |
| `TZ`                 | `UTC`                 | Decides which day counts as "today". Set it. |
| `IGDB_CLIENT_ID`     | (unset)               | Twitch app client id. Enables game search.   |
| `IGDB_CLIENT_SECRET` | (unset)               | Twitch app client secret.                    |

Set `TZ` (for example `Europe/Prague`). It only affects the date prefilled
into a new session, and only near midnight, but on a UTC container that
prefill is a day behind for anyone east of Greenwich. The browser corrects
the first date to its own today while the field is untouched, so this mostly
matters with JavaScript off.

**Game search is optional.** With no IGDB credentials the app runs fine as a
date-only planner: the search box is disabled and games simply aren't offered.
The vote-cookie signing key is generated on first run and kept in the
database, so there is no secret to configure for that.

### Getting IGDB credentials

IGDB is Twitch's game database and authenticates through a Twitch app.

1. Sign in at <https://dev.twitch.tv/console/apps> and **Register Your
   Application**. Name it anything; set OAuth Redirect URL to
   `http://localhost` (unused by this flow); category *Application Integration*.
2. Copy the **Client ID**, then **New Secret** and copy the **Client Secret**.
3. In Coolify, add both as environment variables on the app:
   `IGDB_CLIENT_ID` and `IGDB_CLIENT_SECRET`. Redeploy.

The app fetches and caches its own access token; nothing else to wire up. The
token is used only server-side, never sent to the browser.

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

**Progressive enhancement.** Both boards and the results are server-rendered
and readable without JavaScript. Cycling cells and searching games need JS;
there is a `<noscript>` note saying so. Removing a game works without JS (a
plain form submit); the confirmation prompt is the JS enhancement on top.

**Game metadata is client-supplied and sanitised.** When you add a game the
browser posts the snapshot it got from *our* search endpoint (which came from
IGDB). The server clamps lengths and ranges but trusts the values, consistent
with the nickname-only trust model. The worst case is a wrong year on a game,
never anything worse, because all output is escaped.

**Schema migrations.** The database version lives in `PRAGMA user_version`;
`internal/store/migrations.go` applies steps in order, each in its own
transaction. The original date-only schema is migration 1; migration 2 renames
`polls` to `sessions` and adds the game columns, so an existing deployment's
polls become sessions with an empty game board on first start after deploy.
A released migration is never edited; changes go in a new step.

## Layout

```
main.go                  wiring, config, graceful shutdown
internal/store/          SQLite: schema, migrations, queries, cookie secret
internal/igdb/           IGDB client: token, search, response parsing, cache
internal/i18n/           catalogs, plural rules, date names
internal/app/            routes, handlers, two-board view model, scoring
web/templates/           html/template: new (create), session, error
web/static/              css, js, favicon (embedded in the binary)
```

Templates and assets are embedded via `embed.FS`, so the container is one
static binary plus a data volume.

## Tests

```sh
go test ./...
```

Covers the migration (existing data survives and is idempotent), the IGDB
client against a stub server (parsing, token reuse, query sanitising), Czech
plural boundaries and catalog parity, two-board scoring and the provisional
player-count check, the delete-confirmation gate, and a full HTTP flow
(create, search, add, vote across both boards, remove) with a stubbed IGDB.
A guard test fails if a template silently sanitises a value to `ZgotmplZ`.

## License

MIT. See [LICENSE](LICENSE).
