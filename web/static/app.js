(() => {
  const audio = document.querySelector("#audio");
  const toggle = document.querySelector("#player-toggle");
  const quality = document.querySelector("#quality");
  const seek = document.querySelector("#seek");
  const volume = document.querySelector("#volume");
  const lyrics = document.querySelector("#lyrics");
  const toast = document.querySelector("#toast");
  const state = { queue: [], index: -1 };
  const fullscreen = document.querySelector("#fullscreen-player");
  const toggles = [toggle, document.querySelector("#fullscreen-toggle")];
  const seeks = [seek, document.querySelector("#fullscreen-seek")];
  let lyricsTrackID = "";
  let lyricsAbort;
  let lyricsRetryAt = 0;
  const lyricCache = new Map();
  const lyricsContent = document.querySelector("#lyrics-content");
  let lyricLines = [];
  let lyricIndex = -1;

  const tintArtwork = (image) => {
    const panel = image.closest(".feature-card, .album-hero, .player-dock");
    if (!panel) return;
    const source = image.currentSrc || image.src;
    if (!image.complete || !image.naturalWidth || new URL(source, location.href).pathname === "/static/cover-placeholder.svg") {
      panel.style.removeProperty("--cover-image");
      return;
    }
    panel.style.setProperty("--cover-image", `url(${JSON.stringify(source)})`);
  };

  const showToast = (message) => {
    toast.textContent = message;
    toast.classList.add("toast-message");
    window.clearTimeout(showToast.timer);
    showToast.timer = window.setTimeout(() => {
      toast.textContent = "";
      toast.classList.remove("toast-message");
    }, 4500);
  };

  const timeLabel = (seconds) => {
    if (!Number.isFinite(seconds) || seconds < 0) return "--:--";
    const value = Math.floor(seconds);
    return `${Math.floor(value / 60)}:${String(value % 60).padStart(2, "0")}`;
  };

  const paintPlayer = () => {
    const playing = !audio.paused && !audio.ended && !audio.error;
    const loading = playing && audio.readyState < audio.HAVE_FUTURE_DATA;
    toggles.forEach(button => {
      button.textContent = playing ? "Ⅱ" : "▶";
      button.setAttribute("aria-label", playing ? "Pause" : "Play");
      button.setAttribute("aria-pressed", String(playing));
      button.setAttribute("aria-busy", String(loading));
      button.classList.toggle("is-loading", loading);
    });
    document.querySelector("#stream-quality").textContent = loading ? "LOADING" : audio.dataset.trackId ? quality.value.toUpperCase() : "READY";
  };

  const paintTracks = () => {
    document.querySelectorAll("#view .play-track").forEach((button) => {
      const current = button.dataset.id === audio.dataset.trackId;
      const playing = current && !audio.paused && !audio.ended && !audio.error;
      const row = button.closest(".track-row");
      row.classList.toggle("is-current", current);
      button.classList.toggle("is-loading", playing && audio.readyState < audio.HAVE_FUTURE_DATA);
      button.setAttribute("aria-busy", String(playing && audio.readyState < audio.HAVE_FUTURE_DATA));
      button.textContent = playing ? "Ⅱ" : "▶";
      const label = `${playing ? "Pause" : "Play"} ${button.dataset.name}`;
      button.setAttribute("aria-label", label);
      button.setAttribute("aria-pressed", String(playing));
      const title = row.querySelector(".track-title");
      title?.setAttribute("aria-label", label);
      title?.setAttribute("aria-pressed", String(playing));
    });
  };

  const syncView = () => {
    const url = new URL(location.href);
    const heading = document.querySelector("#view h1");
    if (heading) document.title = `${heading.textContent.trim()} · flac_dl`;
    document.querySelector(".search-form input").value = url.searchParams.get("q") || "";
    document.querySelectorAll(".primary-nav a").forEach((link) => {
      const active = link.getAttribute("href") === url.pathname || (url.pathname === "/" && link.getAttribute("href") === "/charts/songs");
      link.classList.toggle("active", active);
      if (active) link.setAttribute("aria-current", "page");
      else link.removeAttribute("aria-current");
    });
    paintTracks();
    document.querySelectorAll(".feature-card img, .album-hero img").forEach(tintArtwork);
  };

  const currentTrack = () => state.queue[state.index];

  const paintFullscreen = () => {
    const item = currentTrack();
    if (!item) return;
    document.querySelector("#fullscreen-title").textContent = item.name;
    document.querySelector("#fullscreen-artist").textContent = item.artist;
    const cover = document.querySelector("#fullscreen-artwork");
    if (cover.dataset.trackId !== item.id) {
      cover.dataset.trackId = item.id;
      cover.dataset.fallbackTried = "";
      cover.dataset.fallback = item.fallback || "";
      const source = document.querySelector("#player-art").src;
      cover.src = source;
      const large = source.replace("/albumcover/120/", "/albumcover/500/");
      if (large !== source) {
        const upgrade = new Image();
        upgrade.onload = () => {
          if (currentTrack()?.id === item.id && cover.dataset.trackId === item.id) cover.src = large;
        };
        upgrade.src = large;
      }
      cover.alt = `${item.name} cover`;
    }
  };

  const setLyricsMessage = (message) => {
    lyricLines = [];
    lyricIndex = -1;
    lyricsContent.classList.remove("is-synced");
    lyricsContent.textContent = message;
  };

  const paintLyrics = () => {
    if (!fullscreen.open || !lyricLines.length) return;
    let index = lyricLines.length - 1;
    while (index >= 0 && lyricLines[index].time > audio.currentTime) index--;
    if (index === lyricIndex) return;
    const previous = lyricLines[lyricIndex]?.element;
    previous?.classList.remove("is-active");
    previous?.removeAttribute("aria-current");
    lyricIndex = index;
    const line = lyricLines[index]?.element;
    if (!line) {
      lyricsContent.scrollTo({ top: 0, behavior: "auto" });
      return;
    }
    line.classList.add("is-active");
    line.setAttribute("aria-current", "true");
    lyricsContent.scrollTo({
      top: Math.max(0, line.offsetTop - lyricsContent.clientHeight / 2 + line.offsetHeight / 2),
      behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth",
    });
  };

  const showLyrics = (result) => {
    setLyricsMessage("");
    if (result.instrumental) {
      setLyricsMessage("Instrumental · no lyrics.");
      return;
    }
    const timed = [];
    if (typeof result.syncedLyrics === "string") {
      for (const line of result.syncedLyrics.split(/\r?\n/)) {
        const timestamp = /\[(\d+):([0-5]?\d)(?:[.:](\d{1,3}))?\]/g;
        const text = line.replace(timestamp, "").trim();
        for (const match of line.matchAll(timestamp)) {
          timed.push({ time: Number(match[1]) * 60 + Number(match[2]) + Number(`0.${match[3] || "0"}`), text });
        }
      }
    }
    timed.sort((a, b) => a.time - b.time);
    if (!timed.length) {
      setLyricsMessage((typeof result.plainLyrics === "string" && result.plainLyrics.trim()) || "No lyrics found for this track. Enjoy the music.");
      return;
    }
    lyricsContent.classList.add("is-synced");
    lyricLines = timed.map(line => {
      const element = document.createElement("span");
      element.className = "lyric-line";
      element.textContent = line.text || "♪";
      lyricsContent.append(element);
      return { time: line.time, element };
    });
    paintLyrics();
  };

  const loadLyrics = async () => {
    const item = currentTrack();
    if (!fullscreen.open || !item || lyricsTrackID === item.id) return;
    lyricsAbort?.abort();
    lyricsTrackID = item.id;
    lyricsContent.scrollTop = 0;
    const cached = lyricCache.get(item.id);
    if (cached && cached.expires > Date.now()) {
      showLyrics(cached.result);
      return;
    }
    if (Date.now() < lyricsRetryAt) {
      lyricsTrackID = "";
      setLyricsMessage("The lyrics service is busy. Please try again later.");
      return;
    }
    setLyricsMessage("Finding lyrics…");
    const controller = lyricsAbort = new AbortController();
    const url = new URL("https://lrclib.net/api/get");
    url.searchParams.set("track_name", item.name);
    url.searchParams.set("artist_name", item.artist);
    if (item.album) url.searchParams.set("album_name", item.album);
    const duration = Math.round(audio.duration);
    if (duration >= 1 && duration <= 3600) url.searchParams.set("duration", String(duration));
    try {
      const response = await fetch(url, {
        headers: { "Lrclib-Client": "flac_dl/0.1.0 (https://github.com/rishavnandi/flac_dl)" },
        signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
      });
      if (controller.signal.aborted || audio.dataset.trackId !== item.id) return;
      if (response.status === 404) {
        setLyricsMessage("No lyrics found for this track. Enjoy the music.");
        return;
      }
      if (response.status === 429 || response.status === 503) {
        const delay = Math.max(1, Number(response.headers.get("Retry-After")) || 60);
        lyricsRetryAt = Date.now() + delay * 1000;
        lyricsTrackID = "";
        setLyricsMessage("The lyrics service is busy. Please try again later.");
        return;
      }
      if (!response.ok) throw new Error("Lyrics unavailable");
      const result = await response.json();
      if (controller.signal.aborted || audio.dataset.trackId !== item.id) return;
      if (result?.instrumental || (typeof result?.plainLyrics === "string" && result.plainLyrics.trim()) ||
          (typeof result?.syncedLyrics === "string" && result.syncedLyrics.trim())) {
        lyricCache.delete(item.id);
        if (lyricCache.size >= 64) lyricCache.delete(lyricCache.keys().next().value);
        lyricCache.set(item.id, { result, expires: Date.now() + 86400000 });
      }
      showLyrics(result);
    } catch {
      if (!controller.signal.aborted && audio.dataset.trackId === item.id) {
        lyricsTrackID = "";
        setLyricsMessage("Lyrics are unavailable right now. Playback is unaffected.");
      }
    }
  };

  const playTrack = async (item) => {
    if (!item || !item.id) return;
    if (audio.dataset.trackId !== item.id) {
      lyricsAbort?.abort();
      lyricsTrackID = "";
      setLyricsMessage("Lyrics appear here when available.");
    }
    document.querySelector("#player-title").textContent = item.name || "Unknown track";
    document.querySelector("#player-artist").textContent = item.artist || "Kuwo";
    const art = document.querySelector("#player-art");
    art.closest(".player-dock").style.removeProperty("--cover-image");
    art.dataset.fallbackTried = "";
    art.dataset.fallback = item.fallback || "";
    art.src = item.art || item.fallback || "/static/cover-placeholder.svg";
    audio.dataset.trackId = item.id;
    document.querySelector(".expand-player").disabled = false;
    paintFullscreen();
    audio.src = `/stream?id=${encodeURIComponent(item.id)}&q=${encodeURIComponent(quality.value)}`;
    audio.load();
    paintTracks();
    loadLyrics();
    try {
      await audio.play();
    } catch (err) {
      if (audio.dataset.trackId === item.id && err.name !== "AbortError") {
        showToast(`Could not start playback: ${err.message || "stream unavailable"}`);
      }
    }
    paintPlayer();
  };

  const queueFromPage = (button) => {
    const buttons = [...document.querySelectorAll("#view .play-track")];
    state.queue = buttons.map((node) => ({
      id: node.dataset.id,
      name: node.dataset.name,
      artist: node.dataset.artist,
      album: node.dataset.album,
      art: node.dataset.art,
      fallback: node.dataset.fallback,
    }));
    state.index = Math.max(0, buttons.indexOf(button));
  };

  const playRelative = (delta) => {
    if (!state.queue.length) return;
    state.index = (state.index + delta + state.queue.length) % state.queue.length;
    playTrack(currentTrack());
  };

  document.addEventListener("click", (event) => {
    const download = event.target.closest(".browser-download");
    if (download) {
      const url = new URL(download.href);
      url.searchParams.set("q", quality.value);
      download.href = url.href;
      return;
    }
    const playButton = event.target.closest('[data-action="play"]') ||
      event.target.closest('[data-action="play-title"]')?.closest(".track-row")?.querySelector(".play-track");
    if (playButton) {
      event.preventDefault();
      if (audio.dataset.trackId === playButton.dataset.id) {
        if (audio.paused) audio.play().catch((err) => showToast(err.message));
        else audio.pause();
        return;
      }
      queueFromPage(playButton);
      playTrack(currentTrack());
      return;
    }

    const action = event.target.closest("[data-action]")?.dataset.action;
    if (action === "toggle") {
      if (!audio.src) {
        const first = document.querySelector("#view .play-track");
        if (first) {
          queueFromPage(first);
          playTrack(currentTrack());
        } else {
          showToast("Search Kuwo to find a track to play.");
        }
      } else if (audio.paused) {
        audio.play().catch((err) => showToast(err.message));
      } else {
        audio.pause();
      }
    } else if (action === "previous") {
      playRelative(-1);
    } else if (action === "next") {
      playRelative(1);
    } else if (action === "play-first") {
      const first = document.querySelector("#view .play-track");
      if (first) {
        queueFromPage(first);
        playTrack(currentTrack());
      }
    } else if (action === "dismiss-toast") {
      event.target.closest(".download-toast")?.remove();
    } else if (action === "back") {
      history.back();
    } else if (action === "forward") {
      history.forward();
    } else if (action === "expand-player" && currentTrack()) {
      paintFullscreen();
      fullscreen.showModal();
      loadLyrics();
      paintLyrics();
    } else if (action === "close-player") {
      fullscreen.close();
    }
  });

  document.addEventListener("keydown", (event) => {
    if (event.defaultPrevented || event.repeat || event.isComposing) return;
    const editing = event.target.closest("input, textarea, select, button, a, [contenteditable]");
    const searchShortcut = (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k";
    if (searchShortcut || (!editing && !event.metaKey && !event.ctrlKey && !event.altKey && event.key === "/")) {
      event.preventDefault();
      const search = document.querySelector(".search-form input");
      search.focus();
      search.select();
    } else if (!editing && !event.metaKey && !event.ctrlKey && !event.altKey && event.code === "Space") {
      event.preventDefault();
      toggle.click();
    }
  });

  document.addEventListener("error", (event) => {
    const image = event.target;
    if (!(image instanceof HTMLImageElement)) return;
    image.closest(".feature-card, .album-hero, .player-dock")?.style.removeProperty("--cover-image");
    const fallback = image.dataset.fallback;
    if (fallback && !image.dataset.fallbackTried && image.src !== new URL(fallback, location.href).href) {
      image.dataset.fallbackTried = "1";
      image.src = fallback;
    } else if (!image.src.endsWith("/static/cover-placeholder.svg")) {
      image.dataset.fallbackTried = "1";
      image.src = "/static/cover-placeholder.svg";
    }
  }, true);
  document.addEventListener("load", (event) => {
    if (event.target instanceof HTMLImageElement) tintArtwork(event.target);
  }, true);

  for (const event of ["play", "playing", "pause", "waiting", "canplay", "emptied"]) {
    audio.addEventListener(event, () => {
      paintPlayer();
      paintTracks();
    });
  }
  audio.addEventListener("ended", () => {
    if (state.index >= 0 && state.index < state.queue.length - 1) playRelative(1);
    else {
      paintPlayer();
      paintTracks();
    }
  });
  audio.addEventListener("error", () => {
    if (audio.dataset.trackId) showToast("This stream could not be loaded. Try another quality or result.");
    paintPlayer();
    paintTracks();
  });
  audio.addEventListener("timeupdate", () => {
    paintPlayer();
    document.querySelector("#elapsed").textContent = timeLabel(audio.currentTime);
    document.querySelector("#remaining").textContent = timeLabel(audio.duration);
    document.querySelector("#fullscreen-elapsed").textContent = timeLabel(audio.currentTime);
    document.querySelector("#fullscreen-duration").textContent = timeLabel(audio.duration);
    seeks.forEach(range => {
      if (Number.isFinite(audio.duration) && audio.duration > 0 && !range.matches(":active")) {
        range.value = String(Math.round(audio.currentTime / audio.duration * Number(range.max)));
      }
    });
    paintLyrics();
  });
  seeks.forEach(range => range.addEventListener("input", () => {
    if (Number.isFinite(audio.duration) && audio.duration > 0) {
      audio.currentTime = Number(range.value) / Number(range.max) * audio.duration;
      paintLyrics();
    }
  }));
  const paintVolume = () => {
    const percent = `${Math.round(audio.volume * 100)}%`;
    volume.title = percent;
    volume.setAttribute("aria-valuetext", percent);
  };
  volume.addEventListener("input", () => {
    audio.volume = Number(volume.value);
    paintVolume();
    try { localStorage.setItem("flac_dl_volume", volume.value); } catch {}
  });
  audio.volume = Number(volume.value);

  quality.addEventListener("change", () => {
    try { localStorage.setItem("flac_dl_quality", quality.value); } catch {}
    if (audio.dataset.trackId) {
      const item = currentTrack();
      if (item) playTrack(item);
    }
  });
  lyrics?.addEventListener("change", (event) => {
    try { localStorage.setItem("flac_dl_lyrics", event.target.checked ? "1" : "0"); } catch {}
  });
  try {
    const savedQuality = localStorage.getItem("flac_dl_quality");
    if ([...quality.options].some((option) => option.value === savedQuality)) quality.value = savedQuality;
    if (lyrics) lyrics.checked = localStorage.getItem("flac_dl_lyrics") === "1";
    const savedVolume = localStorage.getItem("flac_dl_volume");
    if (savedVolume !== null && savedVolume.trim() !== "" && Number.isFinite(Number(savedVolume))) {
      audio.volume = Math.min(1, Math.max(0, Number(savedVolume)));
      volume.value = String(audio.volume);
    }
  } catch {}
  paintVolume();

  document.body.addEventListener("htmx:responseError", () => showToast("That request failed. Please try again."));
  document.body.addEventListener("htmx:sendError", () => showToast("Connection lost. Check that flac_dl is still running."));
  document.body.addEventListener("htmx:afterSettle", (event) => {
    if (event.detail.target?.id === "view") syncView();
  });
  document.body.addEventListener("htmx:historyRestore", syncView);
  document.body.addEventListener("htmx:beforeRequest", (event) => {
    if (event.detail.elt.matches(".download-form, .album-actions form")) {
      toast.classList.remove("toast-message");
    }
  });
  syncView();
  tintArtwork(document.querySelector("#player-art"));
})();
