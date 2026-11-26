import { test, expect } from "bun:test";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";

const source = readFileSync(new URL("../web/static/app.js", import.meta.url), "utf8");

function app(saved = {}, options = {}) {
  const element = () => ({
    listeners: {}, dataset: {}, value: "0.8", textContent: "", attributes: {},
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
    focus() { this.focused = true; },
    select() { this.selected = true; },
    showModal() { this.open = true; },
    close() { this.open = false; },
  });
  const nodes = new Map();
  const node = selector => {
    if (!nodes.has(selector)) {
      const item = element();
      item.classList = {
        add: name => item.classes.add(name),
        remove: name => item.classes.delete(name),
        toggle: (name, active) => active ? item.classes.add(name) : item.classes.delete(name),
      };
      nodes.set(selector, item);
    }
    return nodes.get(selector);
  };
  const audio = node("#audio");
  audio.paused = true;
  audio.play = async () => { audio.paused = false; audio.emit("play"); };
  audio.pause = () => { audio.paused = true; audio.emit("pause"); };
  audio.load = () => {};
  node("#player-art").closest = () => node("#player-dock");
  node("#seek").max = node("#fullscreen-seek").max = "1000";
  node("#quality").options = [{ value: "auto" }, { value: "flac" }];
  node("#quality").value = "auto";
  const track = node(".test-track");
  track.dataset = { id: "42", name: "Test Song" };
  track.closest = () => node(".test-row");
  const nav = [node(".home"), node(".albums")];
  nav[0].attributes.href = "/charts/songs";
  nav[1].attributes.href = "/charts/albums";
  const document = {
    ...element(), body: element(),
    querySelector: node,
    querySelectorAll: selector => selector === "#view .play-track" ? [track] : selector === ".primary-nav a" ? nav : [],
  };
  const storage = new Map(Object.entries(saved));
  const location = { href: "http://localhost/search?q=tron" };
  const Image = class {};
  runInNewContext(source, {
    document, location, URL, history: {},
    AbortController, AbortSignal,
    fetch: options.fetch,
    HTMLImageElement: Image,
    localStorage: { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value) },
    window: { clearTimeout() {}, setTimeout() {} },
  });
  const click = action => document.emit("click", {
    target: {
      closest: selector => selector === '[data-action="play"]' && action === "play" ? track :
        selector === "[data-action]" ? { dataset: { action } } : null,
    },
    preventDefault() {},
  });
  return { document, node, audio, storage, location, track, nav, Image, click };
}

test("volume persists, validates saved values, and accepts mute", () => {
  for (const [saved, expected] of [["0", 0], ["0.4", 0.4], ["3", 1], ["-1", 0], ["bad", 0.8], ["", 0.8]]) {
    const { audio, node, storage } = app({ flac_dl_volume: saved });
    expect(audio.volume).toBe(expected);
    node("#volume").value = "0.25";
    node("#volume").emit("input");
    expect(audio.volume).toBe(0.25);
    expect(storage.get("flac_dl_volume")).toBe("0.25");
  }
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
  const { click, node, audio, track } = app({}, {
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
