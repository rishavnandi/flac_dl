# flac_dl

A command line client for [flac.music.hi.cn](https://flac.music.hi.cn), a
multi-site lossless music search. Search Kuwo and NetEase, then download FLAC,
320k or 128k files with their lyrics.

The site is a React SPA over a small JSON API. This tool talks to the same API,
so search, album and playlist handling behave exactly like the web page, without
a browser and without its download modals.

Not affiliated with the site, Kuwo or NetEase. Personal use only. Do not
redistribute what you download.

## Install

Requires Go 1.22 or newer.

```sh
git clone <this repo> flac_dl
cd flac_dl
go build -o ~/.local/bin/flac_dl .
```

`~/.local/bin` is on `PATH` on this machine. For a one-off build, `go build .`
leaves a `flac_dl` binary in the current directory.

## Usage

```sh
# search; the number on the left is the --take index, the id is on the right
flac_dl search "tron legacy"

# search and download the first result as FLAC
flac_dl search "tron legacy" --take 1 -q flac -o ~/Music

# download one track by id or by page link
flac_dl get 2407050 -o ~/Music
flac_dl get https://www.kuwo.cn/play_detail/2407050 --lrc

# a whole album or playlist, six downloads in parallel
flac_dl album 215821 -j 6 -o ~/Music
flac_dl album https://www.kuwo.cn/playlist_detail/3823890 -q 320

# a direct CDN link, keeping its file name
flac_dl get 'https://other-er.kuwo.cn/.../F000002Cv1ef1RJlHs.flac' -o ~/Music
```

Files are written as `Artist - Title.flac`, `Artist - Title.mp3` and
`Artist - Title.lrc`. Existing files are skipped unless `--force` is given.

### search

| flag | default | meaning |
| --- | --- | --- |
| `-p` | `kuwo` | platform: `kuwo` or `wyy` |
| `-n` | `20` | results to list; pages past the API's 20 per request |
| `--json` | off | print the raw result list as JSON |
| `--take` | | download these result numbers, e.g. `1,4` |
| `-q` | `auto` | quality for `--take`: `auto`, `flac`, `320`, `128` |
| `-o` | `.` | output directory |
| `--lrc` | off | also download lyrics |
| `--force` | off | overwrite existing files |

### get

| flag | default | meaning |
| --- | --- | --- |
| `-p` | `kuwo` | platform for a bare id |
| `-q` | `auto` | `auto`, `flac`, `320`, `128` |
| `-o` | `.` | output directory |
| `--lrc` | off | also download lyrics |
| `--force` | off | overwrite existing files |

### album

Same flags as `get`, plus `-j` (default `4`) for parallel downloads. A bare
number is read as an album id and falls back to a playlist id if the album call
finds nothing; a page link states its own kind.

### Quality

`auto` picks FLAC when the track has it, otherwise 320k, otherwise the last
format offered. An explicit `-q` is strict: a track without that format is
reported with the formats it does have, and skipped during an album download.

## How it works

Every action is a form-encoded `POST` to `https://flac.music.hi.cn/ajax.php`:

| `act` | parameters | returns |
| --- | --- | --- |
| `search` | platform, keyword, page, size | 20 tracks per page, total |
| `album` | platform, id | album info and every track |
| `playlist` | platform, id, page, size | playlist info and a page of tracks |
| `song` | platform, id | one track |
| `getUrl` | platform, songid, format, bitrate, time, sign | a CDN URL |
| `getLyric` | platform, songid, time, sign | LRC text |

Each track row carries a server-issued `time` and `sign`. `getUrl` rejects a bad
pair (`code -2`) and an expired one (`code -3`), so the tool never caches
signatures or links: it resolves fresh for every download. CDN URLs are
tokenised and short-lived, which is why a failed transfer is retried from a new
resolve rather than resumed.

Downloads go to `name.part` and are renamed only after the byte count matches
`Content-Length`, so a dropped connection cannot leave a half file with a
plausible name.

## Limits

- Everything depends on `flac.music.hi.cn` staying up. It is a third-party
  scraper front end with no promise of stability.
- The site's NetEase backend is broken (`接口请求失败` or empty results), so
  `-p wyy` rarely works. Kuwo is reliable.
- The API caps a search page at 20 rows; `-n` above 20 costs extra requests.
- No resume. Partial files are deleted and the track is fetched again.
- Only file names are set. Kuwo's FLACs already carry `TITLE`, `ARTIST` and
  `ALBUM` Vorbis comments, so no tagging step is needed.

## Development

```sh
go test ./...
go vet ./...
```

The tests replay canned API responses through `httptest`, so they need no
network.

## License

MIT. See [LICENSE](LICENSE).
