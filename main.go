package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const version = "0.1.0"

func main() {
	if os.Getenv("VERCEL") == "1" {
		if err := runHostedWeb(os.Getenv("PORT")); err != nil {
			fmt.Fprintln(os.Stderr, "flac_dl: "+err.Error())
			os.Exit(1)
		}
		return
	}
	if len(os.Args) < 2 {
		if err := runWeb(webDefaultAddr); err != nil {
			fmt.Fprintln(os.Stderr, "flac_dl: "+err.Error())
			os.Exit(1)
		}
		return
	}
	var err error
	switch os.Args[1] {
	case "web":
		err = cmdWeb(os.Args[2:])
	case "tui":
		err = runTUI()
	case "search":
		err = cmdSearch(os.Args[2:])
	case "get":
		err = cmdGet(os.Args[2:])
	case "album":
		err = cmdAlbum(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println("flac_dl " + version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "flac_dl: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "flac_dl: "+err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `flac_dl - search and download lossless music

usage:
  flac_dl                    open the local web app
  flac_dl web [-addr 127.0.0.1:8765]
  flac_dl tui                 open the terminal UI
  flac_dl search <keywords> [-p kuwo|wyy] [-n 20] [--json]
  flac_dl search <keywords> --take 1,4 [-q flac] [-o dir] [--lrc]
  flac_dl get <song id | page url | cdn url> [-q auto|flac|320|128] [-o dir] [--lrc] [--force]
  flac_dl album <album or playlist id | page url> [-q auto|flac|320|128] [-o dir] [-j 4] [--lrc] [--force]

the TUI downloads into ./downloads; the subcommands write to -o (default .).
files are named "Artist - Title.flac".
`)
}

func cmdWeb(args []string) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	addr := fs.String("addr", webDefaultAddr, "loopback address to serve on")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: flac_dl web [-addr 127.0.0.1:8765]")
	}
	return runWeb(*addr)
}

func cmdSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	platform := fs.String("p", "kuwo", "platform: kuwo or wyy")
	limit := fs.Int("n", 20, "how many results")
	asJSON := fs.Bool("json", false, "print results as json")
	take := fs.String("take", "", "download these result numbers, e.g. 1,4")
	quality := fs.String("q", "auto", "quality: auto, flac, 320, 128")
	dir := fs.String("o", ".", "output directory")
	lrc := fs.Bool("lrc", false, "also download lyrics")
	force := fs.Bool("force", false, "overwrite existing files")
	flags, positional := splitFlags(args)
	fs.Parse(flags)

	keyword := strings.Join(positional, " ")
	if keyword == "" {
		return errors.New("usage: flac_dl search <keywords>")
	}
	if *platform != "kuwo" && *platform != "wyy" {
		return fmt.Errorf("unknown platform %q, want kuwo or wyy", *platform)
	}
	if *limit < 1 {
		*limit = pageSize
	}

	c := newClient()
	tracks, total, err := c.search(*platform, keyword, *limit)
	if err != nil {
		if *platform == "wyy" {
			return fmt.Errorf("%w (the site's netease backend is unreliable, try -p kuwo)", err)
		}
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Platform string  `json:"platform"`
			Total    int     `json:"total"`
			List     []track `json:"list"`
		}{*platform, total, tracks})
	}
	if len(tracks) == 0 {
		fmt.Fprintln(os.Stderr, "no results")
		return nil
	}

	for i, t := range tracks {
		best, _ := pickQuality(t, "auto")
		fmt.Printf("%3d  %s · %s · %s · %s\n",
			i+1, label(t), clock(t.Duration), strings.ToUpper(best.Format), string(t.ID))
	}
	fmt.Fprintf(os.Stderr, "%d of %d results\n", len(tracks), total)

	if *take == "" {
		return nil
	}
	idx, err := parseIndices(*take, len(tracks))
	if err != nil {
		return err
	}
	jobs := make([]job, 0, len(idx))
	for _, i := range idx {
		t := tracks[i]
		q, err := pickQuality(t, *quality)
		if err != nil {
			return fmt.Errorf("%s: %w", label(t), err)
		}
		jobs = append(jobs, job{platform: *platform, track: t, quality: q, dir: *dir, lrc: *lrc, force: *force})
	}
	return runJobs(c, jobs, 4)
}

func cmdGet(args []string) error {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	platform := fs.String("p", "kuwo", "platform for bare ids: kuwo or wyy")
	quality := fs.String("q", "auto", "quality: auto, flac, 320, 128")
	dir := fs.String("o", ".", "output directory")
	lrc := fs.Bool("lrc", false, "also download lyrics")
	force := fs.Bool("force", false, "overwrite existing files")
	flags, positional := splitFlags(args)
	fs.Parse(flags)
	if len(positional) != 1 {
		return errors.New("usage: flac_dl get <song id | page url | cdn url>")
	}

	arg := positional[0]
	tgt, err := parseTarget(arg, *platform, "song")
	if err != nil {
		return err
	}
	c := newClient()

	if tgt.kind == "album" || tgt.kind == "playlist" {
		return fmt.Errorf("%s url, use: flac_dl album %s", tgt.kind, arg)
	}
	if tgt.kind == "direct" {
		return downloadDirect(c, tgt.url, *dir, *force)
	}

	col, err := c.collection("song", tgt.platform, tgt.id)
	if err != nil {
		return err
	}
	if len(col.List) == 0 {
		return fmt.Errorf("no song with id %s", tgt.id)
	}
	t := col.List[0]
	q, err := pickQuality(t, *quality)
	if err != nil {
		return fmt.Errorf("%s: %w", label(t), err)
	}
	return runJobs(c, []job{{platform: tgt.platform, track: t, quality: q, dir: *dir, lrc: *lrc, force: *force}}, 1)
}

func cmdAlbum(args []string) error {
	fs := flag.NewFlagSet("album", flag.ExitOnError)
	platform := fs.String("p", "kuwo", "platform for bare ids: kuwo or wyy")
	quality := fs.String("q", "auto", "quality: auto, flac, 320, 128")
	dir := fs.String("o", ".", "output directory")
	workers := fs.Int("j", 4, "parallel downloads")
	lrc := fs.Bool("lrc", false, "also download lyrics")
	force := fs.Bool("force", false, "overwrite existing files")
	flags, positional := splitFlags(args)
	fs.Parse(flags)
	if len(positional) != 1 {
		return errors.New("usage: flac_dl album <album or playlist id | page url>")
	}

	arg := positional[0]
	tgt, err := parseTarget(arg, *platform, "album")
	if err != nil {
		return err
	}
	if tgt.kind == "song" {
		return fmt.Errorf("that is a song, use: flac_dl get %s", arg)
	}
	if tgt.kind == "direct" {
		return errors.New("album wants an album or playlist, not a file url")
	}

	c := newClient()
	col, err := c.collection(tgt.kind, tgt.platform, tgt.id)
	// A bare number is read as an album id; playlists have no distinct shape,
	// so fall back once before giving up.
	if err != nil && digits.MatchString(tgt.id) {
		if alt, altErr := c.collection("playlist", tgt.platform, tgt.id); altErr == nil && len(alt.List) > 0 {
			col, err = alt, nil
		}
	}
	if err != nil {
		return err
	}
	if len(col.List) == 0 {
		return fmt.Errorf("no tracks in %s %s", tgt.kind, tgt.id)
	}

	name := tgt.kind + " " + tgt.id
	if col.Info != nil && col.Info.Name != "" {
		name = col.Info.Name
	}
	fmt.Fprintf(os.Stderr, "%s: %s, %d tracks\n", tgt.kind, name, len(col.List))

	jobs := make([]job, 0, len(col.List))
	for _, t := range col.List {
		q, err := pickQuality(t, *quality)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", label(t), err)
			continue
		}
		jobs = append(jobs, job{platform: tgt.platform, track: t, quality: q, dir: *dir, lrc: *lrc, force: *force})
	}
	return runJobs(c, jobs, *workers)
}

func downloadDirect(c *client, link, dir string, force bool) error {
	if strings.HasSuffix(link, "/") {
		return errors.New("url has no file name")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, filepath.Base(link))
	if !force {
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			fmt.Fprintf(os.Stderr, "= %s (exists)\n", path)
			return nil
		}
	}
	if err := newDownloader(c).get(link, path); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "+ %s\n", path)
	return nil
}

func parseIndices(spec string, max int) ([]int, error) {
	var out []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > max {
			return nil, fmt.Errorf("--take %q: want numbers between 1 and %d", part, max)
		}
		out = append(out, n-1)
	}
	if len(out) == 0 {
		return nil, errors.New("--take: no numbers given")
	}
	return out, nil
}

var noValueFlags = map[string]bool{
	"-json": true, "--json": true,
	"-lrc": true, "--lrc": true,
	"-force": true, "--force": true,
	"-h": true, "--help": true,
}

// splitFlags lets flags follow positional arguments, which flag.Parse alone
// does not allow, so both "search -n 5 tron" and "search tron -n 5" work.
func splitFlags(args []string) (flags, positional []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-" || !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		if strings.Contains(a, "=") || noValueFlags[a] || i+1 >= len(args) {
			continue
		}
		i++
		flags = append(flags, args[i])
	}
	return flags, positional
}
