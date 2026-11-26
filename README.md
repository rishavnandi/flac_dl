# flac_dl

[Live site](https://loki.mist-matrix.ts.net:10000) · [Source](https://github.com/rishavnandi/flac_dl)

A Go music browser and downloader for [flac.music.hi.cn](https://flac.music.hi.cn).
Browse Apple Music's US charts, search Kuwo, explore albums, stream tracks, and
download FLAC, 320k or 128k audio with optional lyrics.

The default app is a Go `net/http` site with HTMX and a built-in browser audio
player. It uses an AMOLED black and monochrome interface inspired by Monochrome Music.
The Bubble Tea terminal UI and the non-interactive commands remain available.

Not affiliated with the site, Kuwo or NetEase. Personal use only. Do not
redistribute what you download.

## Build and run

Requires Go 1.24.2 or newer.

```sh
go build -o ./flac_dl .
./flac_dl
```

The binary stays in this project directory. Running it without arguments starts
the local web app at `127.0.0.1:8765` and opens it in your browser. To choose a
different loopback port, run `./flac_dl web -addr 127.0.0.1:8766`.
`./flac_dl tui` opens the terminal UI. Run `./flac_dl --help` for the download
commands.

## Web app

The home page shows Apple's US top songs. Switch to the albums chart, or search
Kuwo from the header. Chart entries open Kuwo search results. Search results
include cover art, album links, format details, streaming playback and downloads.
Album pages include the full track list, play-all and album download actions.

Chart navigation uses cached feeds and refreshes them in the background after
15 minutes. Apple outages keep the last good chart visible. The disk cache
survives service restarts. Fresh instances use bundled chart snapshots immediately,
so even a cold start does not wait for Apple. The page shows the feed's update date.
Snapshots in `charts/` are fallback data, not a guarantee of current rankings.

Successful pages are cached in the browser and Vercel's CDN: charts for one
minute, searches for five minutes, albums for one hour, and artwork redirects
for one day. Search, album and artwork responses also use a bounded server cache
(128 entries, up to 512 KiB each), with stale responses during background refresh
for up to another day. Full pages, HTMX fragments and history restores have
separate cache entries. Errors, audio URLs and downloads are never cached.
Versioned CSS and JavaScript use immutable browser caching. Successful lyrics
lookups are reused for a day in the current tab (up to 64 tracks).

Use the bottom player for play/pause, track navigation, seeking and volume.
Choose Auto, FLAC, 320 kbps or 128 kbps from the quality menu. Auto prefers
FLAC, then 320-kbps MP3, then the last listed format. The player uses the
browser's audio codecs and device. Local downloads go to `./downloads/`; album
downloads run up to four tracks at once.

Press `/` or `Cmd/Ctrl+K` to focus search. `Space` plays or pauses when you are
not using a form or another control. Volume, quality and lyric preferences are
remembered in your browser. The current track stays highlighted in results.
Home, album links and back/forward navigation keep the player running.
Refreshing the page stops playback.

The featured banner, album header and player use subtle blurred-cover tints.
The tint follows the displayed artwork, including fallback covers. Missing
artwork keeps the interface neutral, and text and controls stay monochrome.

Click the artwork on the left of the player to open a full-window view with
larger cover art, playback controls and seeking. Escape or the close button
returns to browsing without stopping playback. Lyrics appear on the right
(below the player on mobile) when available from [LRCLIB](https://lrclib.net).
They load only when the view is opened and use no API key. Timestamped lyrics
highlight the current line and follow playback and seeks inside the lyrics
pane. Tracks without timestamps use plain text. The lookup sends track
metadata directly to LRCLIB and respects its retry limits.

The upstream can return a short spoken notice instead of the requested song,
even with a successful API status. The shared resolver verifies the returned
song ID and retries a mismatched response before reporting an error.

Cover art uses Kuwo's image URL first and falls back to an exact Apple iTunes
artist/album match when needed. Images load lazily in the browser. The page and
HTMX assets are embedded in the executable. The local server binds to a
loopback address only.

Run `go test -race ./...` for the Go tests and `bun test tests/web_app.test.js`
for the browser-state and keyboard regression tests.

## VPS deployment

The public site supports charts, search, albums, playback and individual track
downloads to the visitor's device. Downloads stream through the server and
are not saved on the server. Full album saving and lyric downloads remain
features of the local app.

Build the web-only Linux binary for Loki's ARM64 architecture:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags=server -trimpath -ldflags="-s -w" -o flac_dl .
```

The `server` build excludes the terminal UI and native audio libraries. A normal
`go build` command still includes the CLI, TUI and local web app.
Install the binary at `/opt/flac_dl/flac_dl`. The included
[`deploy/flac_dl.service`](deploy/flac_dl.service) runs it as an unprivileged
dynamic user, restarts it on failure and starts it on boot.

The public-mode command does not open a browser or start disk download jobs:

```sh
./flac_dl serve -addr 127.0.0.1:8765 -host loki.mist-matrix.ts.net:10000
```

After approving Funnel in Tailscale, expose only the unused HTTPS port:

```sh
sudo tailscale funnel --bg --https=10000 --yes http://127.0.0.1:8765
```

The public address is `https://loki.mist-matrix.ts.net:10000`. Existing private
Tailscale Serve routes on ports 443 and 8443 must not be converted to Funnel.
For another host, change the allowed hostname in the command and service file.
Keep the listener on loopback behind the HTTPS proxy.

The app accepts only its configured public hostname. Audio playback goes directly
to Kuwo's CDN. Browser downloads pass through Loki and count toward Funnel's
bandwidth limit. API and CDN availability still depends on the upstream services.

## TUI controls

| key | action |
| --- | --- |
| `/` | focus search |
| `enter` | find a chart item on Kuwo, or open the selected track's album |
| `1` / `2` | show US top songs / albums |
| `r` | refresh charts |
| `↑` / `↓`, `j` / `k` | move through results |
| `pgup` / `pgdn`, `ctrl+u` / `ctrl+d` | jump through the list |
| `esc` | blur search, return from an album, or return to charts |
| `d` | download the selected track |
| `A` | download the open album |
| `space` | stream or pause/resume the selected Kuwo result |
| `s` | stop playback |
| `tab` | cycle quality: auto, FLAC, 320k, 128k |
| `l` | toggle lyric downloads |
| `?` | show key help |
| `q` | quit |

Search pages load as you move down the results. The chart feed is Apple's public
RSS JSON endpoint and needs no account or token. It provides chart discovery,
not audio playback. Press `enter` on a chart entry to search Kuwo, then press
`space` on a result to stream it through the system audio device. On a wide
terminal, the selected item and its cover art appear in a detail pane. Cover art
uses true-colour ANSI and half-block characters. It tries Kuwo's image URL
first, then Apple's public iTunes Search API for an exact artist/album match.
The selected cover loads first; visible chart, search and album covers then
prefetch sequentially in the background.
Downloads go to `./downloads/`; album downloads use four workers, and existing
files are skipped. The TUI uses the selected quality for playback and downloads.
`auto` prefers FLAC, then 320k MP3, then the last available format. Playback
supports FLAC and MP3 and does not save a copy. The TUI remembers quality and
lyric settings only while it runs. If the search field is focused, `?` opens
help and other typed characters stay in the query.

## Non-interactive commands

```sh
# search and download the first result as FLAC
./flac_dl search "tron legacy" --take 1 -q flac -o ~/Music

# download one track by id or page link
./flac_dl get 2407050 -o ~/Music
./flac_dl get https://www.kuwo.cn/play_detail/2407050 --lrc

# download an album or playlist
./flac_dl album 215821 -j 6 -o ~/Music
./flac_dl album https://www.kuwo.cn/playlist_detail/3823890 -q 320

# download a direct CDN link, keeping its file name
./flac_dl get 'https://other-er.kuwo.cn/.../F000002Cv1ef1RJlHs.flac' -o ~/Music
```

Files are named `Artist - Title.flac`, `Artist - Title.mp3` and
`Artist - Title.lrc`. The TUI uses the same naming rules.

### Search flags

| flag | default | meaning |
| --- | --- | --- |
| `-p` | `kuwo` | platform: `kuwo` or `wyy` |
| `-n` | `20` | number of results; pages past the API's 20 per request |
| `--json` | off | print results as JSON |
| `--take` | | download result numbers, e.g. `1,4` |
| `-q` | `auto` | quality: `auto`, `flac`, `320`, `128` |
| `-o` | `.` | output directory |
| `--lrc` | off | also download lyrics |
| `--force` | off | overwrite existing files |

`get` uses `-p`, `-q`, `-o`, `--lrc` and `--force`. `album` has the same flags
plus `-j` (default `4`) for parallel downloads. A bare album number falls back
to a playlist lookup if the album lookup returns no tracks.

`auto` picks FLAC when available, then 320k, then the best offered fallback. An
explicit quality is strict. Tracks without it are skipped in album downloads.

## How it works

Every action is a form-encoded `POST` to
`https://flac.music.hi.cn/ajax.php`. The app uses `search`, `album`, `playlist`,
`song`, `getUrl` and `getLyric`. Each result includes a server-issued `time` and
`sign`; the app resolves a fresh CDN link for every download and does not cache
the short-lived links.

Audio downloads write to a `.part` file and rename it only after the byte count
matches `Content-Length`. A failed transfer is retried using a fresh link, not
resumed.

## Limits

- The app depends on `flac.music.hi.cn` staying available.
- The site's NetEase backend is unreliable (`接口请求失败` or empty results);
  Kuwo is the dependable platform.
- Cover art is best-effort. Kuwo image URLs can time out; the iTunes fallback is
  rate-limited by Apple and only uses exact artist and album matches.
- Chart listings come from Apple's US storefront. Audio is still resolved by the
  existing Kuwo download API; the chart feed does not provide playable audio.
- Direct CDN links keep their opaque server filename because they do not carry
  track metadata.
- No resume support. Partial files are removed and fetched again.
- Kuwo FLACs already include TITLE, ARTIST and ALBUM tags.

## Development

```sh
go test ./...
go vet ./...
```

API tests use canned responses with `httptest`. TUI tests verify rendered cell
dimensions. Tests do not require network access.

## License

MIT. See [LICENSE](LICENSE).
