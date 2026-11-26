import { test, expect } from "bun:test";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";

const source = readFileSync(new URL("../web/static/app.js", import.meta.url), "utf8");

function app(saved = {}, options = {}) {
  const element = () => {
    const item = {
    listeners: {}, dataset: {}, value: "0.8", attributes: {}, children: [], _text: "",
    get textContent() { return this._text + this.children.map(child => child.textContent).join(""); },
    set textContent(value) { this._text = String(value); this.children = []; },
    append(child) { child.offsetTop = this.children.length * 40; this.children.push(child); },
    offsetHeight: 40, clientHeight: 100,
    scrollTo(options) { this.scrollTop = options.top; },
    classes: new Set(),
    style: {
      values: new Map(),
      setProperty(name, value) { this.values.set(name, value); },
      removeProperty(name) { this.values.delete(name); },
    },
    addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); },
    emit(name, detail = {}) { this.listeners[name]?.forEach(fn => fn(detail)); },
    setAttribute(name, value) { this.attributes[name] = value; },
    getAttribute(name) { return this.attributes[name]; },
    removeAttribute(name) { delete this.attributes[name]; },
    matches() { return false; },
    closest() { return null; },
    querySelector() { return null; },
    focus() { this.focused = true; },
    select() { this.selected = true; },
    showModal() { this.open = true; },
    close() { this.open = false; },
    };
    item.classList = {
      add: name => item.classes.add(name),
      remove: name => item.classes.delete(name),
      toggle: (name, active) => active ? item.classes.add(name) : item.classes.delete(name),
    };
    return item;
  };
  const nodes = new Map();
  const node = selector => {
    if (!nodes.has(selector)) {
      nodes.set(selector, element());
    }
    return nodes.get(selector);
  };
  const audio = node("#audio");
  audio.paused = true;
  audio.readyState = 4;
  audio.HAVE_FUTURE_DATA = 3;
  audio.play = async () => { audio.paused = false; audio.emit("play"); };
  audio.pause = () => { audio.paused = true; audio.emit("pause"); };
  audio.load = () => {};
  node("#player-art").closest = () => node("#player-dock");
  node("#seek").max = node("#fullscreen-seek").max = "1000";
  node("#quality").options = [{ value: "auto" }, { value: "flac" }];
  node("#quality").value = "auto";
  node("#volume").value = "1";
  const track = node(".test-track");
  track.dataset = { id: "42", name: "Test Song" };
  track.closest = () => node(".test-row");
  node(".test-title").closest = () => node(".test-row");
  node(".test-row").querySelector = selector => selector === ".play-track" ? track : selector === ".track-title" ? node(".test-title") : null;
  const nav = [node(".home"), node(".albums")];
  nav[0].attributes.href = "/charts/songs";
  nav[1].attributes.href = "/charts/albums";
  const document = {
    ...element(), body: element(),
    querySelector: node,
    createElement: element,
    querySelectorAll: selector => selector === "#view .play-track" ? [track] : selector === ".primary-nav a" ? nav : [],
  };
  const storage = new Map(Object.entries(saved));
  const location = { href: "http://localhost/search?q=tron" };
  const imageRequests = [];
  const Image = class { constructor() { imageRequests.push(this); } };
  runInNewContext(source, {
    document, location, URL, history: {},
    AbortController, AbortSignal,
    fetch: options.fetch,
    HTMLImageElement: Image, Image,
    localStorage: { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value) },
    window: { clearTimeout() {}, setTimeout() {}, matchMedia: () => ({ matches: true }) },
  });
  const click = action => document.emit("click", {
    target: {
      closest: selector => selector === '[data-action="play"]' && action === "play" ? track :
        selector === '[data-action="play-title"]' && action === "play-title" ? node(".test-title") :
        selector === "[data-action]" ? { dataset: { action } } : null,
    },
    preventDefault() {},
  });
  return { document, node, audio, storage, location, track, nav, Image, click, imageRequests };
}

test("volume persists, validates saved values, and accepts mute", () => {
  expect(app().audio.volume).toBe(1);
  for (const [saved, expected] of [["0", 0], ["0.4", 0.4], ["3", 1], ["-1", 0], ["bad", 1], ["", 1]]) {
    const { audio, node, storage } = app({ flac_dl_volume: saved });
    expect(audio.volume).toBe(expected);
    node("#volume").value = "0.25";
    node("#volume").emit("input");
    expect(audio.volume).toBe(0.25);
    expect(storage.get("flac_dl_volume")).toBe("0.25");
    expect(node("#volume").attributes["aria-valuetext"]).toBe("25%");
  }
});

test("play and pause updates preserve the centered SVG controls", () => {
  const { click, node } = app();
  const icon = node(".test-play-pause-svg");
  node("#player-toggle").append(icon);
  click("play");
  expect(node("#player-toggle").children[0]).toBe(icon);
  expect(node("#player-toggle").attributes["aria-pressed"]).toBe("true");
  click("play");
  expect(node("#player-toggle").children[0]).toBe(icon);
  expect(node("#player-toggle").attributes["aria-pressed"]).toBe("false");
});

test("song titles and cover controls share playback and preserve position when toggling", async () => {
  const { click, audio, node } = app();
  click("play-title");
  await settle();
  expect(audio.dataset.trackId).toBe("42");
  expect(node("#player-title").disabled).toBe(false);
  expect(audio.paused).toBe(false);
  expect(node(".test-title").attributes["aria-label"]).toBe("Pause Test Song");
  audio.currentTime = 27;
  click("play");
  expect(audio.paused).toBe(true);
  expect(node(".test-title").attributes["aria-pressed"]).toBe("false");
  click("play-title");
  await settle();
  expect(audio.paused).toBe(false);
  expect(audio.currentTime).toBe(27);
});

test("buffering feedback clears on playback, pause and stream errors", async () => {
  const { click, audio, node, track } = app();
  audio.readyState = 0;
  click("play-title");
  await settle();
  expect(node("#player-toggle").classes.has("is-loading")).toBe(true);
  expect(track.attributes["aria-busy"]).toBe("true");
  expect(node("#stream-quality").textContent).toBe("LOADING");
  audio.readyState = 4;
  audio.emit("playing");
  expect(track.classes.has("is-loading")).toBe(false);
  expect(node("#player-toggle").attributes["aria-busy"]).toBe("false");
  audio.readyState = 1;
  audio.emit("waiting");
  expect(track.classes.has("is-loading")).toBe(true);
  click("play-title");
  expect(track.classes.has("is-loading")).toBe(false);
  click("play-title");
  audio.error = { code: 2 };
  audio.emit("error");
  expect(track.classes.has("is-loading")).toBe(false);
  expect(node("#player-toggle").classes.has("is-loading")).toBe(false);
});

test("switching tracks while one loads does not report a stale playback error", async () => {
  const { click, audio, track, node } = app();
  let rejectFirst;
  audio.play = () => new Promise((_, reject) => { rejectFirst = reject; });
  click("play");
  track.dataset = { id: "43", name: "New song" };
  audio.play = async () => { audio.paused = false; audio.emit("play"); };
  click("play");
  rejectFirst(new Error("old stream failed"));
  await settle();
  expect(audio.dataset.trackId).toBe("43");
  expect(node("#toast").textContent).toBe("");
});

test("history restoration syncs navigation and track state without touching audio", () => {
  const { document, node, audio, location, nav, track } = app();
  audio.dataset.trackId = "42";
  audio.paused = false;
  audio.currentTime = 19;
  location.href = "http://localhost/charts/albums";
  document.body.emit("htmx:historyRestore");
  expect(nav[1].attributes["aria-current"]).toBe("page");
  expect(nav[0].attributes["aria-current"]).toBeUndefined();
  expect(node(".search-form input").value).toBe("");
  expect(node(".test-row").classes.has("is-current")).toBe(true);
  expect(track.attributes["aria-label"]).toBe("Pause Test Song");
  expect(audio.currentTime).toBe(19);
  expect(audio.paused).toBe(false);
});

test("search shortcuts ignore text entry and space leaves native buttons alone", () => {
  const { document, node } = app();
  const event = (key, editing = false) => ({
    key, code: key === " " ? "Space" : "",
    target: { closest: () => editing ? {} : null },
    preventDefault() { this.prevented = true; },
  });
  let clicks = 0;
  node("#player-toggle").click = () => clicks++;
  document.emit("keydown", event("/", true));
  expect(node(".search-form input").focused).toBeUndefined();
  document.emit("keydown", event("/"));
  expect(node(".search-form input").focused).toBe(true);
  document.emit("keydown", event(" ", true));
  expect(clicks).toBe(0);
  document.emit("keydown", event(" "));
  expect(clicks).toBe(1);
});

test("cover tint follows loaded artwork and clears for missing or placeholder art", () => {
  const { document, node, Image } = app();
  const panel = node(".cover-panel");
  const image = Object.assign(new Image(), {
    src: "https://covers.example/blue.jpg",
    currentSrc: "https://covers.example/blue.jpg",
    complete: true, naturalWidth: 600, dataset: {},
    closest: () => panel,
  });
  document.emit("load", { target: image });
  expect(panel.style.values.get("--cover-image")).toBe('url("https://covers.example/blue.jpg")');
  image.currentSrc = "https://covers.example/red.jpg";
  document.emit("load", { target: image });
  expect(panel.style.values.get("--cover-image")).toBe('url("https://covers.example/red.jpg")');
  image.currentSrc = "http://localhost/static/cover-placeholder.svg";
  document.emit("load", { target: image });
  expect(panel.style.values.has("--cover-image")).toBe(false);
  image.currentSrc = image.src;
  image.naturalWidth = 0;
  document.emit("load", { target: image });
  expect(panel.style.values.has("--cover-image")).toBe(false);
});

test("a failed cover clears the previous tint before trying fallback artwork", () => {
  const { document, node, Image } = app();
  const panel = node(".cover-panel");
  panel.style.setProperty("--cover-image", 'url("old.jpg")');
  const image = Object.assign(new Image(), {
    src: "https://covers.example/missing.jpg",
    dataset: { fallback: "/artwork?artist=test&album=test" },
    closest: () => panel,
  });
  document.emit("error", { target: image });
  expect(panel.style.values.has("--cover-image")).toBe(false);
  expect(image.src).toBe("/artwork?artist=test&album=test");
});

test("hosted download links use the selected quality without replacing the player", () => {
  const { document, node, audio } = app();
  const download = { href: "https://music.vercel.app/stream?id=42&q=auto&download=1" };
  node("#quality").value = "flac";
  audio.currentTime = 19;
  audio.paused = false;
  document.emit("click", { target: { closest: selector => selector === ".browser-download" ? download : null } });
  expect(new URL(download.href).searchParams.get("q")).toBe("flac");
  expect(new URL(download.href).searchParams.get("download")).toBe("1");
  expect(audio.currentTime).toBe(19);
  expect(audio.paused).toBe(false);
});

const settle = () => new Promise(resolve => setImmediate(resolve));

test("full-screen player shares playback, seeks and loads lyrics only on demand", async () => {
  const requests = [];
  const { click, node, audio, track, imageRequests } = app({}, {
    fetch: async (url, init) => {
      requests.push({ url, init });
      return { ok: true, json: async () => ({ plainLyrics: "<b>Lyrics are text, not HTML</b>" }) };
    },
  });
  track.dataset.art = "https://img1.kuwo.cn/star/albumcover/120/test.jpg";
  audio.duration = 197;
  click("play");
  expect(requests.length).toBe(0);
  audio.currentTime = 19;
  click("expand-player");
  await settle();
  expect(node("#fullscreen-player").open).toBe(true);
  expect(node("#fullscreen-title").textContent).toBe("Test Song");
  expect(node("#fullscreen-artwork").src).toBe("https://img1.kuwo.cn/star/albumcover/120/test.jpg");
  expect(imageRequests[0].src).toBe("https://img1.kuwo.cn/star/albumcover/500/test.jpg");
  imageRequests[0].onload();
  expect(node("#fullscreen-artwork").src).toBe("https://img1.kuwo.cn/star/albumcover/500/test.jpg");
  expect(node("#lyrics-content").textContent).toBe("<b>Lyrics are text, not HTML</b>");
  expect(requests[0].url.searchParams.get("track_name")).toBe("Test Song");
  expect(requests[0].url.searchParams.get("duration")).toBe("197");
  expect(requests[0].init.headers["Lrclib-Client"]).toContain("flac_dl");
  expect(audio.paused).toBe(false);
  expect(audio.currentTime).toBe(19);
  node("#fullscreen-seek").value = "500";
  node("#fullscreen-seek").emit("input");
  expect(audio.currentTime).toBe(98.5);
  audio.emit("timeupdate");
  expect(node("#fullscreen-elapsed").textContent).toBe("1:38");
  expect(node("#fullscreen-toggle").attributes["aria-label"]).toBe("Pause");
  click("close-player");
  expect(node("#fullscreen-player").open).toBe(false);
  expect(audio.paused).toBe(false);
  click("expand-player");
  expect(requests.length).toBe(1);
});

test("lyrics from a previous track cannot replace the new track's lyrics", async () => {
  let resolveFirst;
  const requests = [];
  const { click, node, track } = app({}, {
    fetch: async (url, init) => {
      requests.push({ url, init });
      if (requests.length === 1) return new Promise(resolve => { resolveFirst = resolve; });
      return { ok: true, json: async () => ({ plainLyrics: "New song lyrics" }) };
    },
  });
  click("play");
  click("expand-player");
  track.dataset = { id: "43", name: "New Song", artist: "Artist" };
  click("play");
  await settle();
  expect(requests[0].init.signal.aborted).toBe(true);
  resolveFirst({ ok: true, json: async () => ({ plainLyrics: "Old song lyrics" }) });
  await settle();
  expect(node("#lyrics-content").textContent).toBe("New song lyrics");
});

test("missing lyrics and rate limits do not interrupt music or trigger retries", async () => {
  for (const status of [404, 429]) {
    let calls = 0;
    const { click, node, track, audio } = app({}, {
      fetch: async () => {
        calls++;
        return { status, headers: { get: () => "60" } };
      },
    });
    click("play");
    click("expand-player");
    await settle();
    expect(node("#lyrics-content").textContent).toContain(status === 404 ? "No lyrics found" : "service is busy");
    if (status === 429) {
      track.dataset = { id: "43", name: "Another Song" };
      click("play");
      await settle();
    }
    expect(calls).toBe(1);
    expect(audio.paused).toBe(false);
  }
});

test("pending or failed large artwork keeps the thumbnail and stale upgrades are ignored", () => {
  const { click, track, node, imageRequests } = app();
  track.dataset.art = "https://img1.kuwo.cn/star/albumcover/120/first.jpg";
  click("play");
  expect(node("#fullscreen-artwork").src).toBe(track.dataset.art);
  expect(imageRequests[0].src).toContain("/500/first.jpg");
  track.dataset = { id: "43", name: "Next song", art: "https://img1.kuwo.cn/star/albumcover/120/second.jpg" };
  click("play");
  imageRequests[0].onload();
  expect(node("#fullscreen-artwork").src).toBe(track.dataset.art);
  imageRequests[1].onload();
  expect(node("#fullscreen-artwork").src).toContain("/500/second.jpg");
});

test("timestamped lyrics sort, highlight, follow playback and update when seeking backward", async () => {
  const { click, node, audio } = app({}, {
    fetch: async () => ({
      ok: true,
      json: async () => ({
        plainLyrics: "Plain fallback",
        syncedLyrics: "[ar:Test Artist]\n[00:10.25]Second line\n[00:01]Intro\n[00:05.5][00:20:50]Repeated <b>text</b>\n[00:25.123]",
      }),
    }),
  });
  audio.duration = 100;
  audio.currentTime = 0;
  click("play");
  click("expand-player");
  await settle();
  const content = node("#lyrics-content");
  expect(content.classes.has("is-synced")).toBe(true);
  expect(content.children.map(line => line.textContent)).toEqual(["Intro", "Repeated <b>text</b>", "Second line", "Repeated <b>text</b>", "♪"]);
  expect(content.children.some(line => line.classes.has("is-active"))).toBe(false);
  audio.currentTime = 5.49;
  audio.emit("timeupdate");
  expect(content.children[0].attributes["aria-current"]).toBe("true");
  audio.currentTime = 5.5;
  audio.emit("timeupdate");
  expect(content.children[1].attributes["aria-current"]).toBe("true");
  expect(content.children[0].attributes["aria-current"]).toBeUndefined();
  audio.currentTime = 11;
  audio.emit("timeupdate");
  expect(content.children[2].classes.has("is-active")).toBe(true);
  expect(content.scrollTop).toBe(50);
  click("close-player");
  audio.currentTime = 26;
  audio.emit("timeupdate");
  expect(content.children[2].classes.has("is-active")).toBe(true);
  click("expand-player");
  expect(content.children[4].classes.has("is-active")).toBe(true);
  node("#fullscreen-seek").value = "10";
  node("#fullscreen-seek").emit("input");
  expect(audio.currentTime).toBe(1);
  expect(content.children[0].classes.has("is-active")).toBe(true);
  expect(content.children[4].attributes["aria-current"]).toBeUndefined();
  expect(content.scrollTop).toBe(0);
  node("#fullscreen-seek").value = "0";
  node("#fullscreen-seek").emit("input");
  expect(content.children.some(line => line.classes.has("is-active"))).toBe(false);
  expect(content.scrollTop).toBe(0);
});

test("invalid synced lyrics retain the plain-text fallback", async () => {
  const { click, node } = app({}, {
    fetch: async () => ({
      ok: true,
      json: async () => ({ syncedLyrics: "[ar:Artist]\n[00:99]Invalid time", plainLyrics: "Plain lyrics" }),
    }),
  });
  click("play");
  click("expand-player");
  await settle();
  expect(node("#lyrics-content").classes.has("is-synced")).toBe(false);
  expect(node("#lyrics-content").textContent).toBe("Plain lyrics");
});

test("returning to a track reuses successful lyrics without another lookup", async () => {
  let calls = 0;
  const { click, track, node } = app({}, {
    fetch: async () => {
      calls++;
      return { ok: true, json: async () => ({ plainLyrics: `Lyrics ${calls}` }) };
    },
  });
  click("play");
  click("expand-player");
  await settle();
  expect(node("#lyrics-content").textContent).toBe("Lyrics 1");
  track.dataset = { id: "43", name: "Another song" };
  click("play");
  await settle();
  expect(node("#lyrics-content").textContent).toBe("Lyrics 2");
  track.dataset = { id: "42", name: "Test Song" };
  click("play");
  await settle();
  expect(node("#lyrics-content").textContent).toBe("Lyrics 1");
  expect(calls).toBe(2);
});

test("lyric buttons seek while preserving play/pause and ignore removed lines", async () => {
  const { click, audio, node, track } = app({}, {
    fetch: async () => ({
      ok: true,
      json: async () => ({ syncedLyrics: "[00:01]First\n[00:05.50]Second\n[02:00]Beyond duration" }),
    }),
  });
  audio.duration = 100;
  click("play");
  click("expand-player");
  await settle();
  const content = node("#lyrics-content");
  const second = content.children[1];
  expect(second.type).toBe("button");
  const tap = line => content.emit("click", { target: { closest: () => line } });
  tap(second);
  expect(audio.currentTime).toBe(5.5);
  expect(audio.paused).toBe(false);
  expect(second.attributes["aria-current"]).toBe("true");
  audio.pause();
  tap(content.children[0]);
  expect(audio.currentTime).toBe(1);
  expect(audio.paused).toBe(true);
  tap(content.children[2]);
  expect(audio.currentTime).toBe(100);
  audio.duration = NaN;
  tap(second);
  expect(audio.currentTime).toBe(100);
  audio.duration = 100;
  track.dataset = { id: "43", name: "Next song" };
  click("play");
  audio.currentTime = 0;
  tap(second);
  expect(audio.currentTime).toBe(0);
  await settle();
  tap(second);
  expect(audio.currentTime).toBe(0);
});
