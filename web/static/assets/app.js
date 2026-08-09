const EVENT_TYPES = [
  { key: "", label: "Alla" },
  { key: "person", label: "Person" },
  { key: "vehicle", label: "Fordon" },
  { key: "animal", label: "Djur" },
  { key: "face", label: "Ansikte" },
  { key: "licensePlate", label: "Registreringsskylt" },
  { key: "motion", label: "Rörelse" },
  { key: "continuous", label: "Kontinuerlig" },
  { key: "unknown", label: "Okänd" },
];

const state = {
  camera: "",
  day: "",
  type: "",
  clips: [],
  modalIndex: -1,
};

async function api(path, opts) {
  const res = await fetch(path, opts);
  if (res.status === 401) {
    window.location.href = "/login";
    throw new Error("unauthorized");
  }
  return res;
}

function fmtTime(iso) {
  return new Date(iso).toLocaleTimeString("sv-SE", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function fmtDuration(seconds) {
  const m = Math.floor(seconds / 60);
  const s = Math.floor(seconds % 60);
  return `${m}:${String(s).padStart(2, "0")}`;
}

function badgeClass(type) {
  return "badge badge-" + (type || "unknown").toLowerCase().replace(/[^a-z]/g, "");
}

function badgeLabel(type) {
  const found = EVENT_TYPES.find((t) => t.key.toLowerCase() === (type || "").toLowerCase());
  return found ? found.label : type;
}

async function loadCameras() {
  const res = await api("/api/cameras");
  const cams = await res.json();
  const select = document.getElementById("camera-select");
  for (const cam of cams || []) {
    const opt = document.createElement("option");
    opt.value = cam.Key;
    opt.textContent = `${cam.Name} (${cam.ClipCount})`;
    select.appendChild(opt);
  }
}

async function loadDays() {
  const res = await api("/api/days");
  const days = await res.json();
  const select = document.getElementById("day-select");
  select.innerHTML = "";
  for (const day of days || []) {
    const opt = document.createElement("option");
    opt.value = day;
    opt.textContent = day;
    select.appendChild(opt);
  }
  if (days && days.length > 0) {
    state.day = days[0];
    select.value = days[0];
  }
}

function renderTypeFilters() {
  const nav = document.getElementById("type-filters");
  nav.innerHTML = "";
  for (const t of EVENT_TYPES) {
    const chip = document.createElement("button");
    chip.className = "type-chip" + (state.type === t.key ? " active" : "");
    chip.textContent = t.label;
    chip.addEventListener("click", () => {
      state.type = t.key;
      renderTypeFilters();
      fetchClips();
    });
    nav.appendChild(chip);
  }
}

async function fetchClips() {
  const params = new URLSearchParams();
  if (state.day) params.set("day", state.day);
  if (state.camera) params.set("camera", state.camera);
  if (state.type) params.set("type", state.type);

  const res = await api("/api/clips?" + params.toString());
  state.clips = await res.json() || [];
  renderGrid();
}

function renderGrid() {
  const grid = document.getElementById("grid");
  const empty = document.getElementById("empty-state");
  grid.innerHTML = "";

  if (state.clips.length === 0) {
    empty.hidden = false;
    return;
  }
  empty.hidden = true;

  state.clips.forEach((clip, index) => {
    const card = document.createElement("div");
    card.className = "card";
    card.addEventListener("click", () => openModal(index));

    const thumb = document.createElement("div");
    thumb.className = "card-thumb";
    const img = document.createElement("img");
    img.loading = "lazy";
    img.src = clip.thumbnail_url;
    img.alt = "";
    img.onerror = () => {
      img.remove();
      const placeholder = document.createElement("div");
      placeholder.className = "placeholder";
      placeholder.textContent = "Ingen förhandsvisning";
      thumb.appendChild(placeholder);
    };
    thumb.appendChild(img);

    const body = document.createElement("div");
    body.className = "card-body";
    body.innerHTML = `
      <div class="card-time">${fmtTime(clip.start)} · ${fmtDuration(clip.duration_s)}</div>
      <div class="card-meta">
        <span class="card-camera">${escapeHtml(clip.camera)}</span>
        <span class="${badgeClass(clip.event_type)}">${escapeHtml(badgeLabel(clip.event_type))}</span>
      </div>
      ${clip.event_detail ? `<div class="card-detail">${escapeHtml(clip.event_detail)}</div>` : ""}
    `;

    card.appendChild(thumb);
    card.appendChild(body);
    grid.appendChild(card);
  });
}

function escapeHtml(s) {
  const div = document.createElement("div");
  div.textContent = s ?? "";
  return div.innerHTML;
}

function openModal(index) {
  state.modalIndex = index;
  const clip = state.clips[index];
  if (!clip) return;

  const video = document.getElementById("player-video");
  video.src = clip.stream_url;

  document.getElementById("player-camera").textContent = clip.camera;
  const detail = clip.event_detail ? ` · ${clip.event_detail}` : "";
  document.getElementById("player-time").textContent =
    `${clip.day} ${fmtTime(clip.start)} (${fmtDuration(clip.duration_s)}) · ${badgeLabel(clip.event_type)}${detail}`;
  document.getElementById("player-download").href = clip.download_url;

  document.getElementById("player-modal").hidden = false;
}

function closeModal() {
  const video = document.getElementById("player-video");
  video.pause();
  video.removeAttribute("src");
  video.load();
  document.getElementById("player-modal").hidden = true;
  state.modalIndex = -1;
}

function navigateModal(delta) {
  if (state.modalIndex < 0) return;
  const next = state.modalIndex + delta;
  if (next < 0 || next >= state.clips.length) return;
  openModal(next);
}

function wireStaticControls() {
  document.getElementById("camera-select").addEventListener("change", (e) => {
    state.camera = e.target.value;
    fetchClips();
  });
  document.getElementById("day-select").addEventListener("change", (e) => {
    state.day = e.target.value;
    fetchClips();
  });
  document.getElementById("logout-btn").addEventListener("click", async () => {
    await api("/api/logout", { method: "POST" });
    window.location.href = "/login";
  });

  document.getElementById("player-close").addEventListener("click", closeModal);
  document.getElementById("player-prev").addEventListener("click", () => navigateModal(-1));
  document.getElementById("player-next").addEventListener("click", () => navigateModal(1));
  document.querySelector(".modal-backdrop").addEventListener("click", closeModal);

  document.addEventListener("keydown", (e) => {
    if (document.getElementById("player-modal").hidden) return;
    if (e.key === "Escape") closeModal();
    if (e.key === "ArrowLeft") navigateModal(-1);
    if (e.key === "ArrowRight") navigateModal(1);
  });
}

async function init() {
  wireStaticControls();
  renderTypeFilters();
  await Promise.all([loadCameras(), loadDays()]);
  await fetchClips();
}

init().catch((err) => {
  if (err.message !== "unauthorized") console.error(err);
});
