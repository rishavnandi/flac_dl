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
    const playing = !audio.paused;
    toggles.forEach(button => {
      button.textContent = playing ? "Ⅱ" : "▶";
      button.setAttribute("aria-label", playing ? "Pause" : "Play");
    });
    document.querySelector("#stream-quality").textContent = audio.dataset.trackId ? quality.value.toUpperCase() : "READY";
  };

  const paintTracks = () => {
    document.querySelectorAll("#view .play-track").forEach((button) => {
      const current = button.dataset.id === audio.dataset.trackId;
      const playing = current && !audio.paused;
      button.closest(".track-row").classList.toggle("is-current", current);
      button.textContent = playing ? "Ⅱ" : "▶";
      button.setAttribute("aria-label", `${playing ? "Pause" : "Play"} ${button.dataset.name}`);
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
      cover.src = document.querySelector("#player-art").src.replace("/albumcover/120/", "/albumcover/500/");
      cover.alt = `${item.name} cover`;
    }
  };

  const loadLyrics = async () => {
    const item = currentTrack();
    if (!fullscreen.open || !item || lyricsTrackID === item.id) return;
    lyricsAbort?.abort();
    lyricsTrackID = item.id;
    const content = document.querySelector("#lyrics-content");
    content.scrollTop = 0;
    if (Date.now() < lyricsRetryAt) {
      content.textContent = "The lyrics service is busy. Please try again later.";
      return;
    }
    content.textContent = "Finding lyrics…";
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
        content.textContent = "No lyrics found for this track. Enjoy the music.";
        return;
      }
      if (response.status === 429 || response.status === 503) {
        const delay = Math.max(1, Number(response.headers.get("Retry-After")) || 60);
        lyricsRetryAt = Date.now() + delay * 1000;
        lyricsTrackID = "";
        content.textContent = "The lyrics service is busy. Please try again later.";
        return;
      }
      if (!response.ok) throw new Error("Lyrics unavailable");
      const result = await response.json();
      if (controller.signal.aborted || audio.dataset.trackId !== item.id) return;
      content.textContent = result.instrumental ? "Instrumental · no lyrics." :
        (typeof result.plainLyrics === "string" && result.plainLyrics.trim()) || "No lyrics found for this track. Enjoy the music.";
    } catch {
      if (!controller.signal.aborted && audio.dataset.trackId === item.id) {
        lyricsTrackID = "";
        content.textContent = "Lyrics are unavailable right now. Playback is unaffected.";
      }
    }
  };

  const playTrack = async (item) => {
    if (!item || !item.id) return;
    if (audio.dataset.trackId !== item.id) {
      lyricsAbort?.abort();
      lyricsTrackID = "";
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
      showToast(`Could not start playback: ${err.message || "stream unavailable"}`);
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
    const playButton = event.target.closest('[data-action="play"]');
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

  audio.addEventListener("play", paintPlayer);
  audio.addEventListener("playing", paintPlayer);
  audio.addEventListener("pause", paintPlayer);
  audio.addEventListener("play", paintTracks);
  audio.addEventListener("pause", paintTracks);
  audio.addEventListener("ended", () => {
    if (state.index >= 0 && state.index < state.queue.length - 1) playRelative(1);
    else paintPlayer();
  });
  audio.addEventListener("error", () => {
    if (audio.dataset.trackId) showToast("This stream could not be loaded. Try another quality or result.");
    paintPlayer();
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
  });
  seeks.forEach(range => range.addEventListener("input", () => {
    if (Number.isFinite(audio.duration) && audio.duration > 0) {
      audio.currentTime = Number(range.value) / Number(range.max) * audio.duration;
    }
  }));
  volume.addEventListener("input", () => {
    audio.volume = Number(volume.value);
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
