import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { Button } from "./components/ui/button";

const NAV_ITEMS = [
  { id: "grid", label: "Grid" },
  { id: "stats", label: "Stats" },
  { id: "settings", label: "Settings" },
];
const SETTINGS_SECTIONS = ["habits", "storage", "api", "backups"];

const WEEKDAYS = ["Mo", "", "We", "", "Fr", "", ""];
const PICKER_WEEKDAYS = ["Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"];
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const PIXEL_LABELS = ["grey", "green", "orange"];
const PIXEL_COLORS = [undefined, "var(--good)", "var(--rating-2)"];

function pageFromHash() {
  return pageForHash(window.location.hash.slice(1));
}

function settingsSectionFromHash() {
  return settingsSectionForHash(window.location.hash.slice(1));
}

// All browser API calls use this seam so normal-mode requests carry the
// same-origin token while setup-mode requests remain pre-token.
function apiFetch(input, options = {}) {
  const headers = new Headers(options.headers);
  const token = window.__DELTA_TOKEN__;
  if (token) headers.set("Authorization", `Bearer ${token}`);
  return fetch(input, { ...options, headers });
}

async function setupRequest(endpoint, payload) {
  return apiRequest(endpoint, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  }, { fallbackMessage: "setup request failed", fallbackCode: "setup_error" });
}

async function apiRequest(endpoint, options = {}, { fallbackMessage = "API request failed", fallbackCode = "api_error" } = {}) {
  const response = await apiFetch(endpoint, options);
  let body = {};
  try {
    body = await response.json();
  } catch (error) {
    if (error?.name !== "SyntaxError") throw error;
  }
  if (!response.ok) {
    const error = new Error(body.error?.message || fallbackMessage);
    error.code = body.error?.code || fallbackCode;
    throw error;
  }
  return body;
}

function groupedKey(key) {
  return key.match(/.{1,4}/g) || [];
}

function CopyButton({ value, className = "setup-copy", disabled = false, onError }) {
  const [label, setLabel] = useState("copy");

  async function copy() {
    try {
      const nextValue = typeof value === "function" ? await value() : value;
      if (!navigator.clipboard?.writeText) {
        const clipboardError = new Error("clipboard unavailable");
        clipboardError.code = "clipboard_unavailable";
        throw clipboardError;
      }
      await navigator.clipboard.writeText(nextValue ?? "");
      setLabel("copied");
      window.setTimeout(() => setLabel("copy"), 1500);
    } catch (error) {
      setLabel("copy manually");
      onError?.(error);
    }
  }

  return (
    <button className={className} disabled={disabled} onClick={copy} type="button">
      {label}
    </button>
  );
}

function SetupHeader() {
  return <Header setup />;
}

function SetupWizard({ info }) {
  const [step, setStep] = useState("door");
  const [door, setDoor] = useState(null);
  const [path, setPath] = useState(info.default_database_path || "");
  const [key, setKey] = useState("");
  const [saved, setSaved] = useState(false);
  const [openPath, setOpenPath] = useState("");
  const [openKey, setOpenKey] = useState("");
  const [error, setError] = useState(null);
  const [done, setDone] = useState(null);

  function chooseDoor(nextDoor) {
    setDoor(nextDoor);
    setError(null);
    setStep(nextDoor === "create" ? "path" : "openform");
  }

  async function continueCreate() {
    setError(null);
    if (key) {
      setStep("key");
      return;
    }
    try {
      const response = await setupRequest("/api/setup/key", { path });
      setPath(response.database_path || path);
      setKey(response.key);
      setStep("key");
    } catch (requestError) {
      setError({ code: requestError.code, message: requestError.message });
    }
  }

  async function regenerateKey() {
    setError(null);
    try {
      const response = await setupRequest("/api/setup/key", { path, regenerate: true });
      setPath(response.database_path || path);
      setKey(response.key);
      setSaved(false);
      setStep("key");
    } catch (requestError) {
      setError({ code: requestError.code, message: requestError.message });
    }
  }

  async function createDiary() {
    setError(null);
    try {
      const response = await setupRequest("/api/setup/create", { path, key, confirmed: true });
      setDone(response);
      setStep("done");
    } catch (requestError) {
      setError({ code: requestError.code, message: requestError.message });
    }
  }

  async function openDiary() {
    setError(null);
    try {
      const response = await setupRequest("/api/setup/open", { path: openPath, key: openKey });
      setDone(response);
      setStep("done");
    } catch (requestError) {
      setError({ code: requestError.code, message: requestError.message });
    }
  }

  function openDelta() {
    window.location.hash = "#grid";
    window.location.reload();
  }

  let stepNumber = "";
  let content;
  if (step === "door") {
    stepNumber = "1 / …";
    content = (
      <>
        <h2>Welcome. This machine has no diary yet.</h2>
        <p className="setup-lead">DELTA keeps everything in one encrypted SQLite file. Choose how to start:</p>
        <div className="setup-doors">
          <button className="setup-door" onClick={() => chooseDoor("create")} type="button">
            <span className="door-title"><em>create</em> a new diary</span>
            <span className="door-detail">a fresh encrypted file — you pick where it lives</span>
          </button>
          <button className="setup-door" onClick={() => chooseDoor("open")} type="button">
            <span className="door-title"><em>open</em> an existing diary</span>
            <span className="door-detail">point at a .db from another machine (e.g. in iCloud Drive) and paste its key</span>
          </button>
        </div>
      </>
    );
  } else if (step === "path") {
    stepNumber = "2 / 3";
    const defaultPath = info.default_database_path || "";
    const cloudPath = info.icloud_database_path || "";
    content = (
      <>
        <h2>Where should the diary live?</h2>
        <p className="setup-lead">Just a path. Put it inside iCloud Drive and every Mac pointing at it stays in sync.</p>
        <div className="setup-body">
          <label className="setup-field">
            <span>database path</span>
            <input autoFocus onChange={(event) => setPath(event.target.value)} spellCheck="false" value={path} />
          </label>
          <div className="setup-chips">
            <button className={path === defaultPath ? "on" : ""} onClick={() => setPath(defaultPath)} type="button">this Mac only</button>
            <button className={path === cloudPath ? "on" : ""} onClick={() => setPath(cloudPath)} type="button">iCloud Drive · syncs</button>
          </div>
          {error && <p className="setup-error">{error.code}: {error.message}</p>}
          {error?.code === "setup_key_already_shown" && <button className="setup-button" onClick={regenerateKey} type="button">generate a new key</button>}
        </div>
        <div className="setup-nav"><button className="setup-button" onClick={() => setStep("door")} type="button">← back</button><button className="setup-button primary" onClick={continueCreate} type="button">continue →</button></div>
      </>
    );
  } else if (step === "key") {
    stepNumber = "3 / 3";
    content = (
      <>
        <h2>Your encryption key. <b>Shown once.</b></h2>
        <p className="setup-lead">Generated just now. It never leaves this machine except in your password manager.</p>
        <div className="setup-body">
          <div className="setup-keybox" aria-label="encryption key">
            {groupedKey(key).slice(0, 8).join(" ")}<br />{groupedKey(key).slice(8).join(" ")}
          </div>
          <div className="setup-token-line"><CopyButton value={key} /><span>copies the 64 hex chars, no spaces</span></div>
          <p className="setup-warning">Without this key your diary is unrecoverable. There is no reset.</p>
          <label className="setup-check"><input checked={saved} onChange={(event) => setSaved(event.target.checked)} type="checkbox" />I saved this key in my password manager</label>
          {error && <p className="setup-error">{error.code}: {error.message}</p>}
        </div>
        <div className="setup-nav"><button className="setup-button" onClick={() => setStep("path")} type="button">← back</button><button className="setup-button primary" disabled={!saved} onClick={createDiary} type="button">create diary →</button></div>
      </>
    );
  } else if (step === "openform") {
    stepNumber = "2 / 2";
    content = (
      <>
        <h2>Open an existing diary</h2>
        <p className="setup-lead">This is the whole second-machine setup: the file plus its key.</p>
        <div className="setup-body">
          <label className="setup-field"><span>path to the .db file</span><input autoFocus onChange={(event) => setOpenPath(event.target.value)} placeholder={info.icloud_database_path || "path to the .db file"} spellCheck="false" value={openPath} /></label>
          <label className="setup-field"><span>encryption key</span><input onChange={(event) => setOpenKey(event.target.value)} placeholder="paste the 64-hex key — spaces ok" spellCheck="false" value={openKey} /></label>
          {error && <p className="setup-error">{error.message} ({error.code})</p>}
        </div>
        <div className="setup-nav"><button className="setup-button" onClick={() => setStep("door")} type="button">← back</button><button className="setup-button primary" onClick={openDiary} type="button">unlock →</button></div>
      </>
    );
  } else {
    const created = done?.door === "create";
    stepNumber = "";
    content = (
      <>
        <h2 className="setup-good">{created ? "Diary created." : "Diary unlocked."}</h2>
        <p className="setup-lead">{created ? "Everything below is saved — you never need the key again on this machine." : `${done?.entry_count ?? 0} entries · ${done?.first_date || "—"} → ${done?.last_date || "—"}. Key saved to this machine's config.`}</p>
        <div className="setup-body">
          <div className="setup-done-table">
            <span>database</span><span>{done?.database_path}</span>
            <span>config</span><span>{done?.config_path} <small>(0600)</small></span>
            <span>API token</span><span className="setup-token-line"><code>{done?.api_token}</code><CopyButton value={done?.api_token || ""} /></span>
          </div>
          <p className="setup-hint">The token authenticates the CLI and agents against this machine's server — also visible in Settings.</p>
        </div>
        <div className="setup-nav"><span /><button className="setup-button primary" onClick={openDelta} type="button">open DELTA →</button></div>
      </>
    );
  }

  return (
    <div className="setup-app">
      <SetupHeader />
      <div className="setup-wrap"><main className="setup-card">{stepNumber && <span className="setup-step-number">{stepNumber}</span>}{content}</main></div>
    </div>
  );
}

function pageForHash(hash) {
  if (NAV_ITEMS.some((item) => item.id === hash)) return hash;
  return SETTINGS_SECTIONS.includes(hash) ? "settings" : "grid";
}

function settingsSectionForHash(hash) {
  return SETTINGS_SECTIONS.includes(hash) ? hash : "habits";
}

function formatDate(date) {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

function formatLocalTimestamp(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return `${formatDate(date)} ${String(date.getHours()).padStart(2, "0")}:${String(date.getMinutes()).padStart(2, "0")}`;
}

function mondayOffset(date) {
  return (date.getDay() + 6) % 7;
}

function calendarFor(year) {
  const first = new Date(year, 0, 1);
  const offset = mondayOffset(first);
  const start = new Date(year, 0, 1 - offset);
  const last = new Date(year, 11, 31);
  const cells = [];
  const monthMarks = [];
  let date = new Date(start);
  let week = 0;
  while (date <= last || date.getDay() !== 1) {
    const inYear = date.getFullYear() === year;
    if (inYear && date.getDate() === 1) {
      monthMarks.push({ month: date.getMonth(), week });
    }
    cells.push({ date: new Date(date), inYear, key: formatDate(date) });
    date.setDate(date.getDate() + 1);
    if (date.getDay() === 1) week += 1;
  }

  return { cells, monthMarks, weeks: week };
}

const WIZARD_STEPS = [
  {
    id: "freeform",
    label: "freeform",
    title: "How was your day?",
    hint: "freeform — write as much or as little as you like",
    isFilled: (entry) => Boolean(entry.text.trim()),
  },
  {
    id: "day",
    label: "goals · grat · 3 ws",
    isFilled: (entry) =>
      entry.goals.some((goal) => goal.text.trim() || goal.checked) ||
      entry.gratitudes.some((gratitude) => gratitude.trim()) ||
      Object.values(entry.ws).some((value) => value.trim()),
  },
  {
    id: "ratings",
    label: "ratings & habits",
    title: "Ratings & habits",
    hint: "Total is the felt verdict, not an average",
    isFilled: (entry, checkedHabits) => Object.values(entry.ratings).some((value) => value != null) || entry.work_hours != null || checkedHabits.size > 0,
  },
  { id: "save", label: "save", isFilled: () => false },
];

const FREEFORM_STEP = WIZARD_STEPS.findIndex((wizardStep) => wizardStep.id === "freeform");
const DAY_STEP = WIZARD_STEPS.findIndex((wizardStep) => wizardStep.id === "day");
const RATINGS_STEP = WIZARD_STEPS.findIndex((wizardStep) => wizardStep.id === "ratings");

function parseDateKey(key) {
  const [year, month, day] = key.split("-").map(Number);
  return new Date(year, month - 1, day);
}

function nextDateKey(key) {
  const date = parseDateKey(key);
  date.setDate(date.getDate() + 1);
  return formatDate(date);
}

function localClock() {
  const now = new Date();
  return `${String(now.getHours()).padStart(2, "0")}:${String(now.getMinutes()).padStart(2, "0")}`;
}

function blankGoals() {
  return Array.from({ length: 5 }, () => ({ text: "", checked: false }));
}

function blankEntry(date) {
  return {
    date,
    text: "",
    goals: blankGoals(),
    gratitudes: ["", "", ""],
    ws: { went_well: "", could_have_gone_better: "", goal_for_tomorrow: "" },
    ratings: { total: null, body: null, mind: null, spirit: null },
    checkoffs: [],
    pixel: 0,
    work_hours: null,
  };
}

function normalizeEntry(value, date) {
  const source = value || {};
  const empty = blankEntry(date);
  return {
    ...empty,
    ...source,
    date: source.date || date,
    goals: Array.from({ length: 5 }, (_, index) => ({
      ...empty.goals[index],
      ...(source.goals?.[index] || {}),
    })),
    gratitudes: Array.from({ length: 3 }, (_, index) => source.gratitudes?.[index] || ""),
    ws: { ...empty.ws, ...(source.ws || {}) },
    ratings: { ...empty.ratings, ...(source.ratings || {}) },
    checkoffs: (source.checkoffs || []).map(String),
    pixel: source.pixel ?? 0,
    // The API omits work_hours entirely when unset — it is never sent as 0, so
    // an absent key has to normalise to null and not to a recorded zero.
    work_hours: source.work_hours ?? null,
  };
}

function entryPatch(entry) {
  return {
    text: entry.text,
    goals: entry.goals,
    gratitudes: entry.gratitudes,
    ws: entry.ws,
    ratings: entry.ratings,
    pixel: entry.pixel,
    work_hours: entry.work_hours,
  };
}

function mergePatch(previous, next) {
  return {
    ...previous,
    ...next,
    ws: next.ws ? { ...(previous.ws || {}), ...next.ws } : previous.ws,
    ratings: next.ratings ? { ...(previous.ratings || {}), ...next.ratings } : previous.ratings,
  };
}

async function apiJSON(input, options = {}) {
  const response = await apiFetch(input, options);
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(body.error?.message || "API request failed");
    error.code = body.error?.code || "api_error";
    throw error;
  }
  return body;
}

function dateIsInHabitRange(habit, date) {
  return (habit.ranges || []).some((range) => range.active_from <= date && (!range.active_to || range.active_to >= date));
}

function RatingSlider({ label, value, onChange }) {
  return (
    <div className="wizard-rating-row">
      <span className="wizard-rating-label">{label}</span>
      <div className="wizard-rating-cells" role="group" aria-label={`${label} rating`}>
        {[1, 2, 3, 4, 5].map((cell) => (
          <button
            aria-label={`${label} ${cell} of 5`}
            className={value != null && cell <= value ? "lit" : ""}
            key={cell}
            onClick={() => onChange(value === cell ? null : cell)}
            type="button"
          />
        ))}
      </div>
      <span className="wizard-rating-value">{value == null ? "—" : `${value}/5`}</span>
    </div>
  );
}

const WORK_HOURS_MAX = 24;

// Work hours are free decimals, so the field keeps the raw text: a half-typed
// "7." has to survive re-render, and only a parseable in-range value (or an
// empty field, which clears back to unset) is ever pushed to the API.
function workHoursInputValue(hours) {
  return hours == null ? "" : String(hours);
}

function WorkHoursInput({ text, onChange }) {
  return (
    <label className="wizard-rating-row">
      <span className="wizard-rating-label">Work</span>
      <input
        className="wizard-work-hours-input"
        inputMode="decimal"
        onChange={(event) => onChange(event.target.value)}
        placeholder="—"
        value={text}
      />
      <span className="wizard-rating-value">hrs</span>
    </label>
  );
}

function MonthPicker({ picker, selectedDate, entryDates, onNavigate, onSelect, onClose }) {
  const first = new Date(picker.year, picker.month, 1);
  const offset = mondayOffset(first);
  const daysInMonth = new Date(picker.year, picker.month + 1, 0).getDate();
  const today = formatDate(new Date());
  const days = [];
  for (let index = 0; index < offset; index += 1) days.push(<span className="date-picker-pad" key={`pad-${index}`} />);
  for (let day = 1; day <= daysInMonth; day += 1) {
    const key = formatDate(new Date(picker.year, picker.month, day));
    days.push(
      <button
        className={`date-picker-day${entryDates.has(key) ? " has-entry" : ""}${key === today ? " today" : ""}${key === selectedDate ? " selected" : ""}`}
        key={key}
        onClick={() => onSelect(key)}
        type="button"
      >
        {day}
      </button>,
    );
  }
  return (
    <div className="date-picker" role="dialog" aria-label="Choose entry date">
      <span className="date-picker-title">entry date</span>
      <div className="date-picker-header">
        <button aria-label="Previous year" onClick={() => onNavigate({ year: picker.year - 1, month: picker.month })} type="button">«</button>
        <button aria-label="Previous month" onClick={() => onNavigate({ year: picker.month === 0 ? picker.year - 1 : picker.year, month: picker.month === 0 ? 11 : picker.month - 1 })} type="button">‹</button>
        <span>{MONTHS[picker.month]} {picker.year}</span>
        <button aria-label="Next month" onClick={() => onNavigate({ year: picker.month === 11 ? picker.year + 1 : picker.year, month: picker.month === 11 ? 0 : picker.month + 1 })} type="button">›</button>
        <button aria-label="Next year" onClick={() => onNavigate({ year: picker.year + 1, month: picker.month })} type="button">»</button>
      </div>
      <div className="date-picker-grid">
        {PICKER_WEEKDAYS.map((weekday) => <span className="date-picker-weekday" key={weekday}>{weekday}</span>)}
        {days}
      </div>
      <button className="date-picker-cancel" onClick={onClose} type="button">cancel</button>
    </div>
  );
}

function weekdayForDate(date) {
  return ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"][parseDateKey(date).getDay()];
}

// Mirrors the pre-DELTA plain-text journal format that cmd/delta-import parses.
function formatEntryExport(entry) {
  const lines = ["—".repeat(85), entry.date, weekdayForDate(entry.date), "", entry.text, ""];
  lines.push("I'm Grateful for:");
  for (const gratitude of entry.gratitudes) if (gratitude) lines.push(`- ${gratitude}`);
  lines.push("", "Daily Goals:");
  for (const goal of entry.goals) if (goal.text) lines.push(`${goal.checked ? "[x]" : "[ ]"} ${goal.text}`);
  lines.push("", "What went well today?", entry.ws.went_well);
  lines.push("", "What could have gone better today?", entry.ws.could_have_gone_better);
  lines.push("", "What are my goals for tomorrow?", entry.ws.goal_for_tomorrow);
  return lines.join("\n");
}

function PopupRating({ label, value }) {
  return (
    <div className="entry-popup-rating-row">
      <span className="entry-popup-rating-label">{label}</span>
      <span className="entry-popup-rating-cells" aria-label={`${label} rating`}>
        {[1, 2, 3, 4, 5].map((cell) => (
          <span className={`entry-popup-rating-cell${value != null && cell <= value ? ` ${ratingRampClass(value)}` : ""}`} key={cell} />
        ))}
      </span>
      <span className="entry-popup-rating-value">{value == null ? "—" : `${value}/5`}</span>
    </div>
  );
}

function EntryPopup({ date, onClose, onEdit, onDeleted }) {
  const [entry, setEntry] = useState(null);
  const [habits, setHabits] = useState([]);
  const [habitsOpen, setHabitsOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [habitsLoading, setHabitsLoading] = useState(true);
  const [error, setError] = useState("");
  const [habitsError, setHabitsError] = useState("");
  const [loadAttempt, setLoadAttempt] = useState(0);
  const [deleting, setDeleting] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(timer);
  }, [copied]);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    apiJSON(`/api/entries/${date}`)
      .then((body) => {
        if (active) setEntry(normalizeEntry(body, date));
      })
      .catch((requestError) => {
        if (!active) return;
        if (requestError.code === "entry_not_found") {
          onDeleted();
        } else {
          setError(requestError.message);
        }
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => { active = false; };
  }, [date, loadAttempt]);

  useEffect(() => {
    let active = true;
    setHabitsLoading(true);
    setHabitsError("");
    apiJSON("/api/habits")
      .then((body) => {
        if (active) setHabits(Array.isArray(body) ? body : []);
      })
      .catch((requestError) => {
        if (active) setHabitsError(requestError.message);
      })
      .finally(() => {
        if (active) setHabitsLoading(false);
      });
    return () => { active = false; };
  }, [date]);

  useEffect(() => {
    function onKeyDown(event) {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  async function deleteEntry() {
    if (deleting || loading || !entry) return;
    setDeleting(true);
    setError("");
    try {
      await apiJSON(`/api/entries/${date}`, { method: "DELETE" });
      onDeleted();
    } catch (requestError) {
      setError(requestError.message);
      setDeleting(false);
    }
  }

  async function exportEntry() {
    if (loading || !entry) return;
    try {
      await navigator.clipboard.writeText(formatEntryExport(entry));
      setCopied(true);
    } catch {
      setError("could not copy to clipboard");
    }
  }

  const activeHabits = habits.filter((habit) => dateIsInHabitRange(habit, date));
  const checkedGoals = entry?.goals.filter((goal) => goal.checked).length || 0;
  const habitTotal = habitsLoading ? "…" : activeHabits.length;
  const characterCount = entry ? Array.from(entry.text).length : 0;

  // The backdrop carries no click handler on purpose: Escape and the Close
  // button are the only ways out, so a stray click never drops the popup.
  return (
    <div className="overlay" data-entry-popup>
      <div aria-label={`Entry for ${date}`} aria-modal="true" className="wizard-modal entry-popup-modal" role="dialog">
        <div className="entry-popup-body">
          <aside className="entry-popup-left">
            <div className="entry-popup-date-line"><EntryPixel rating={entry?.ratings?.total} /><span className="entry-popup-date">{date}</span><span className="entry-popup-weekday">{weekdayForDate(date)}</span></div>
            {loading && <p className="wizard-muted">loading entry…</p>}
            {!loading && error && (
              <>
                <p className="wizard-error">{error}</p>
                <button className="wizard-button" onClick={() => setLoadAttempt((attempt) => attempt + 1)} type="button">retry</button>
              </>
            )}
            {!loading && !error && entry && (
              <>
                <section className="entry-popup-section">
                  <h2>ratings</h2>
                  <div className="entry-popup-ratings">
                    {[["total", "Total"], ["body", "Body"], ["mind", "Mind"], ["spirit", "Spirit"]].map(([field, label]) => <PopupRating key={field} label={label} value={entry.ratings[field]} />)}
                    {entry.work_hours != null && (
                      <div className="entry-popup-rating-row">
                        <span className="entry-popup-rating-label">Work</span>
                        <span className="entry-popup-work-hours">{entry.work_hours} hrs</span>
                      </div>
                    )}
                  </div>
                </section>
                <section className="entry-popup-section">
                  <h2>goals · {checkedGoals}/5</h2>
                  <div className="entry-popup-goals">
                    {entry.goals.map((goal, index) => (
                      <div className="entry-popup-goal" key={index}>
                        <span className={`entry-popup-check${goal.checked ? " checked" : ""}`}>{goal.checked ? "[x]" : "[ ]"}</span>
                        <span className={goal.checked ? "completed" : ""}>{goal.text || "—"}</span>
                      </div>
                    ))}
                  </div>
                </section>
                <section className="entry-popup-section entry-popup-habits-section">
                  <button aria-expanded={habitsOpen} className="entry-popup-collapser" onClick={() => setHabitsOpen((open) => !open)} type="button">
                    habits · {entry.checkoffs.length}/{habitTotal} {habitsOpen ? "[−]" : "[+]"}
                  </button>
                  {habitsOpen && (
                    <div className="entry-popup-habits">
                      {habitsError && <span className="wizard-muted">{habitsError}</span>}
                      {!habitsError && activeHabits.length === 0 && <span className="wizard-muted">no habits active on this date</span>}
                      {!habitsError && activeHabits.map((habit) => {
                        const checked = entry.checkoffs.includes(String(habit.id));
                        return <span className={checked ? "checked" : ""} key={habit.id}>{checked ? "[x]" : "[ ]"} {habit.name}</span>;
                      })}
                    </div>
                  )}
                </section>
                <section className="entry-popup-section">
                  <h2>gratitudes</h2>
                  <div className="entry-popup-lines">
                    {entry.gratitudes.map((gratitude, index) => <div className="entry-popup-line entry-popup-line-dashed" key={index}><span>-</span><span>{gratitude || "—"}</span></div>)}
                  </div>
                </section>
                <section className="entry-popup-section">
                  <h2>3 ws</h2>
                  <div className="entry-popup-lines">
                    <div className="entry-popup-line entry-popup-line-stacked"><span>went well</span><span>{entry.ws.went_well || "—"}</span></div>
                    <div className="entry-popup-line entry-popup-line-stacked"><span>could be better</span><span>{entry.ws.could_have_gone_better || "—"}</span></div>
                    <div className="entry-popup-line entry-popup-line-stacked"><span>goal tomorrow</span><span>{entry.ws.goal_for_tomorrow || "—"}</span></div>
                  </div>
                </section>
              </>
            )}
          </aside>
          <section className="entry-popup-right">
            <h2>freeform</h2>
            {loading && <p className="wizard-muted">loading entry…</p>}
            {!loading && error && <p className="wizard-error">{error}</p>}
            {!loading && !error && entry && <p className="entry-popup-freeform">{entry.text || "—"}</p>}
          </section>
        </div>
        <footer className="wizard-footer entry-popup-footer">
          <span className="entry-popup-character-count">{entry ? `${characterCount.toLocaleString()} chars` : "— chars"}</span>
          <div className="entry-popup-actions">
            <button className="wizard-button" onClick={onClose} type="button">Close</button>
            <span className="entry-popup-export-wrap">
              {copied && <span className="entry-popup-copied">copied</span>}
              <button className="wizard-button" disabled={loading || !entry} onClick={exportEntry} type="button">Export</button>
            </span>
            <button className="wizard-button entry-popup-delete" disabled={deleting || loading || !entry} onClick={deleteEntry} type="button">{deleting ? "Deleting…" : "Delete"}</button>
            <Button className="wizard-button primary" disabled={loading || !entry} onClick={() => onEdit(date)} type="button">Edit</Button>
          </div>
        </footer>
      </div>
    </div>
  );
}

function EntryWizard({ initialDate, onClose }) {
  const [date, setDate] = useState(initialDate);
  const [entry, setEntry] = useState(blankEntry(initialDate));
  const [workHoursText, setWorkHoursText] = useState("");
  const [habits, setHabits] = useState([]);
  const [step, setStep] = useState(0);
  const [picker, setPicker] = useState(null);
  const [entryDates, setEntryDates] = useState(new Set());
  const [habitsOpen, setHabitsOpen] = useState(true);
  const [loading, setLoading] = useState(true);
  const [entryLoadError, setEntryLoadError] = useState("");
  const [entryLoadAttempt, setEntryLoadAttempt] = useState(0);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [autosaved, setAutosaved] = useState("");
  const [phase, setPhase] = useState("entry");
  const [draftDate, setDraftDate] = useState("");
  const [draftGoals, setDraftGoals] = useState(blankGoals());
  const [draftLoading, setDraftLoading] = useState(false);
  const [draftLoadError, setDraftLoadError] = useState("");
  const [draftLoadAttempt, setDraftLoadAttempt] = useState(0);
  const pendingRef = useRef(new Map());
  const timerRef = useRef(null);
  const inFlightRef = useRef(new Set());
  const persistSequenceRef = useRef(new Map());
  const activeDateRef = useRef(initialDate);
  const phaseRef = useRef("entry");
  const draftDateRef = useRef("");
  const entryRef = useRef(entry);
  const dateRef = useRef(date);
  const keyActionsRef = useRef(null);
  const paneRef = useRef(null);
  const pendingFocusRef = useRef(null);

  phaseRef.current = phase;
  draftDateRef.current = draftDate;
  entryRef.current = entry;
  dateRef.current = date;

  useEffect(() => {
    let active = true;
    setLoading(true);
    setEntryLoadError("");
    setError("");
    apiJSON(`/api/entries/${date}`)
      .then((body) => {
        if (active) {
          const loaded = normalizeEntry(body, date);
          setEntry(loaded);
          setWorkHoursText(workHoursInputValue(loaded.work_hours));
          setEntryLoadError("");
        }
      })
      .catch((requestError) => {
        if (!active) return;
        if (requestError.code === "entry_not_found") {
          setEntry(blankEntry(date));
          setWorkHoursText("");
          setEntryLoadError("");
        } else {
          setEntryLoadError(requestError.message);
        }
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => { active = false; };
  }, [date, entryLoadAttempt]);

  useEffect(() => {
    let active = true;
    apiJSON("/api/habits")
      .then((body) => {
        if (active) setHabits(Array.isArray(body) ? body : []);
      })
      .catch((requestError) => {
        if (active) setError(requestError.message);
      });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    if (phase !== "tomorrow" || !draftDate) return undefined;
    let active = true;
    setDraftLoading(true);
    setDraftLoadError("");
    apiJSON(`/api/entries/${draftDate}`)
      .then((body) => {
        if (active) setDraftGoals(normalizeEntry(body, draftDate).goals);
      })
      .catch((requestError) => {
        if (!active) return;
        if (requestError.code === "entry_not_found") setDraftGoals(blankGoals());
        else setDraftLoadError(requestError.message);
      })
      .finally(() => {
        if (active) setDraftLoading(false);
      });
    return () => { active = false; };
  }, [phase, draftDate, draftLoadAttempt]);

  useEffect(() => {
    if (!picker) return undefined;
    let active = true;
    const from = `${picker.year}-01-01`;
    const to = `${picker.year}-12-31`;
    apiJSON(`/api/entries?from=${from}&to=${to}`)
      .then((body) => {
        if (active) setEntryDates(new Set((Array.isArray(body) ? body : []).map((item) => item.date)));
      })
      .catch((requestError) => {
        if (active) setError(requestError.message);
      });
    return () => { active = false; };
  }, [picker?.year, picker?.month]);

  useEffect(() => () => {
    if (timerRef.current) window.clearTimeout(timerRef.current);
  }, []);

  function persistPatch(targetDate, patch) {
    const requestId = (persistSequenceRef.current.get(targetDate) || 0) + 1;
    persistSequenceRef.current.set(targetDate, requestId);
    const request = apiJSON(`/api/entries/${targetDate}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }).then((body) => {
      setAutosaved(localClock());
      const isLatestRequest = persistSequenceRef.current.get(targetDate) === requestId;
      const hasNewerPending = pendingRef.current.has(targetDate);
      if (isLatestRequest && !hasNewerPending) {
        if (phaseRef.current === "entry" && targetDate === activeDateRef.current) setEntry(normalizeEntry(body, targetDate));
        if (phaseRef.current === "tomorrow" && targetDate === draftDateRef.current) setDraftGoals(normalizeEntry(body, targetDate).goals);
      }
      return body;
    })
      .catch((requestError) => {
        if (requestError && typeof requestError === "object") requestError.persistRequestId = requestId;
        throw requestError;
      });
    inFlightRef.current.add(request);
    request.then(
      () => inFlightRef.current.delete(request),
      () => inFlightRef.current.delete(request),
    );
    return request;
  }

  function restorePending(targetDate, patch, requestId) {
    if (requestId != null && persistSequenceRef.current.get(targetDate) !== requestId) return;
    const newerPending = pendingRef.current.get(targetDate);
    pendingRef.current.set(targetDate, mergePatch(patch, newerPending || {}));
  }

  async function waitForInFlight() {
    while (inFlightRef.current.size > 0) {
      const results = await Promise.allSettled([...inFlightRef.current]);
      const failure = results.find((result) => result.status === "rejected");
      if (failure) throw failure.reason;
    }
  }

  async function flushAutosave() {
    if (timerRef.current) window.clearTimeout(timerRef.current);
    timerRef.current = null;
    const pending = [...pendingRef.current.entries()];
    pendingRef.current.clear();
    if (pending.length === 0) return null;
    try {
      const results = await Promise.all(pending.map(async ([targetDate, patch]) => {
        try {
          return await persistPatch(targetDate, patch);
        } catch (requestError) {
          restorePending(targetDate, patch, requestError.persistRequestId);
          throw requestError;
        }
      }));
      return results.length === 1 ? results[0] : results;
    } catch (requestError) {
      setError(requestError.message);
      throw requestError;
    }
  }

  function schedulePatch(targetDate, patch) {
    const pending = pendingRef.current.get(targetDate);
    pendingRef.current.set(targetDate, mergePatch(pending || {}, patch));
    if (timerRef.current) window.clearTimeout(timerRef.current);
    timerRef.current = window.setTimeout(() => {
      timerRef.current = null;
      void flushAutosave().then(undefined, (requestError) => setError(requestError.message));
    }, 500);
  }

  async function closeWizard() {
    try {
      await flushAutosave();
      await waitForInFlight();
      if (pendingRef.current.size > 0) {
        await flushAutosave();
        await waitForInFlight();
      }
      onClose();
    } catch (requestError) {
      setError(requestError.message);
      try {
        await waitForInFlight();
      } catch (inFlightError) {
        setError(inFlightError.message);
      }
      // Keep the wizard open when an autosave failed; closing must not hide a write error.
    }
  }

  async function selectDate(nextDate) {
    try {
      await flushAutosave();
      await waitForInFlight();
      activeDateRef.current = nextDate;
      setPicker(null);
      setDate(nextDate);
      setStep(0);
    } catch (requestError) {
      setError(requestError.message);
    }
  }

  function updateEntry(nextEntry, patch) {
    setEntry(nextEntry);
    schedulePatch(date, patch);
  }

  function updateGoal(index, field, value) {
    const goals = entry.goals.map((goal, goalIndex) => goalIndex === index ? { ...goal, [field]: value } : goal);
    updateEntry({ ...entry, goals }, { goals });
  }

  function updateGratitude(index, value) {
    const gratitudes = entry.gratitudes.map((gratitude, gratitudeIndex) => gratitudeIndex === index ? value : gratitude);
    updateEntry({ ...entry, gratitudes }, { gratitudes });
  }

  function updateWs(field, value) {
    const ws = { ...entry.ws, [field]: value };
    updateEntry({ ...entry, ws }, { ws });
  }

  function updateRating(field, value) {
    const ratings = { ...entry.ratings, [field]: value };
    updateEntry({ ...entry, ratings }, { ratings });
  }

  function updateWorkHours(text) {
    setWorkHoursText(text);
    if (text.trim() === "") {
      updateEntry({ ...entry, work_hours: null }, { work_hours: null });
      return;
    }
    const value = Number(text);
    // Out-of-range or unparseable text stays visible but is never persisted —
    // the last valid value survives until the field reads as a number again.
    if (!Number.isFinite(value) || value < 0 || value > WORK_HOURS_MAX) return;
    updateEntry({ ...entry, work_hours: value }, { work_hours: value });
  }

  function cyclePixel() {
    const next = (entry.pixel + 1) % 3;
    updateEntry({ ...entry, pixel: next }, { pixel: next });
  }

  async function toggleHabit(habit) {
    if (loading || entryLoadError) return;
    const id = String(habit.id);
    const checked = !entry.checkoffs.includes(id);
    setError("");
    try {
      const body = await apiJSON(`/api/entries/${date}/checkoffs/${habit.id}`, { method: checked ? "POST" : "DELETE" });
      setEntry(normalizeEntry(body, date));
      setAutosaved(localClock());
    } catch (requestError) {
      setError(requestError.message);
    }
  }

  async function saveEntry() {
    if (loading || entryLoadError || draftLoading || draftLoadError) return;
    setSaving(true);
    setError("");
    try {
      await flushAutosave();
      await waitForInFlight();
      await flushAutosave();
      await waitForInFlight();
      const saveDate = dateRef.current;
      const patch = entryPatch(entryRef.current);
      try {
        await persistPatch(saveDate, patch);
      } catch (requestError) {
        restorePending(saveDate, patch, requestError.persistRequestId);
        throw requestError;
      }
      // Evaluate today at save time: an entry opened before midnight becomes a past-day close after midnight.
      if (saveDate === formatDate(new Date())) {
        const nextDate = nextDateKey(saveDate);
        setDraftDate(nextDate);
        setDraftGoals(blankGoals());
        setDraftLoadError("");
        setPhase("tomorrow");
      } else {
        onClose();
      }
    } catch (requestError) {
      setError(requestError.message);
    } finally {
      setSaving(false);
    }
  }

  function updateDraftGoal(index, value) {
    if (draftLoading || draftLoadError) return;
    const goals = draftGoals.map((goal, goalIndex) => goalIndex === index ? { ...goal, text: value } : goal);
    setDraftGoals(goals);
    schedulePatch(draftDate, { goals });
  }

  async function finishDraft() {
    if (draftLoading || draftLoadError) return;
    try {
      await flushAutosave();
      await waitForInFlight();
      onClose();
    } catch (requestError) {
      setError(requestError.message);
      // Keep the draft open when a typed goal could not be saved.
    }
  }

  useEffect(() => {
    function onKeyDown(event) {
      if (event.key === "Escape") {
        event.preventDefault();
        if (picker) setPicker(null);
        else void keyActionsRef.current.closeWizard();
        return;
      }
      if (event.key !== "Enter" || event.shiftKey || picker || loading || entryLoadError || draftLoading || draftLoadError || saving) return;
      if (event.target?.tagName === "TEXTAREA" || event.target?.closest?.("button, a, input, select, textarea, [contenteditable='true'], [role='button'], [role='link']")) return;
      if (phase === "tomorrow") {
        event.preventDefault();
        void keyActionsRef.current.finishDraft();
      } else if (step < WIZARD_STEPS.length - 1) {
        event.preventDefault();
        setStep((current) => current + 1);
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [draftLoadError, draftLoading, entryLoadError, loading, phase, picker, saving, step]);

  // Tab seams set pendingFocusRef before switching steps; once the new pane is
  // in the DOM this places the caret. Sidebar clicks leave the ref null and
  // keep their current (no forced focus) behavior.
  useEffect(() => {
    const focus = pendingFocusRef.current;
    if (!focus) return;
    pendingFocusRef.current = null;
    const pane = paneRef.current;
    if (!pane) return;
    const stepId = WIZARD_STEPS[step].id;
    if (stepId === "freeform") {
      const textarea = pane.querySelector("textarea");
      if (textarea) {
        textarea.focus();
        textarea.setSelectionRange(textarea.value.length, textarea.value.length);
      }
    } else if (stepId === "day") {
      const target = focus === "last"
        ? [...pane.querySelectorAll(".wizard-ws-input input")].pop()
        : pane.querySelector(".wizard-check");
      target?.focus();
    } else if (stepId === "ratings") {
      pane.querySelector(".wizard-rating-cells button")?.focus();
    }
  }, [step]);

  function seamTo(event, index, focus) {
    event.preventDefault();
    pendingFocusRef.current = focus;
    setStep(index);
  }

  // Tab crosses step boundaries: freeform → day (first goal check), last W →
  // ratings, with Shift+Tab mirroring both seams. Everything between the seams
  // is native tab order.
  function onPaneKeyDown(event) {
    if (event.key !== "Tab") return;
    const pane = paneRef.current;
    if (!pane) return;
    const stepId = WIZARD_STEPS[step].id;
    const target = event.target;
    if (stepId === "freeform" && !event.shiftKey && target.tagName === "TEXTAREA") {
      seamTo(event, DAY_STEP, "first");
    } else if (stepId === "day") {
      const checks = pane.querySelectorAll(".wizard-check");
      const wsInputs = pane.querySelectorAll(".wizard-ws-input input");
      if (event.shiftKey && target === checks[0]) seamTo(event, FREEFORM_STEP, "last");
      else if (!event.shiftKey && target === wsInputs[wsInputs.length - 1]) seamTo(event, RATINGS_STEP, "first");
    } else if (stepId === "ratings" && event.shiftKey && target === pane.querySelector(".wizard-rating-cells button")) {
      seamTo(event, DAY_STEP, "last");
    }
  }

  const activeHabits = habits.filter((habit) => dateIsInHabitRange(habit, date));
  const checkedHabits = new Set(entry.checkoffs);
  const filled = Object.fromEntries(WIZARD_STEPS.map((wizardStep) => [wizardStep.id, wizardStep.isFilled(entry, checkedHabits)]));

  keyActionsRef.current = { closeWizard, finishDraft };

  function openPicker() {
    const current = parseDateKey(date);
    setPicker((value) => value || { year: current.getFullYear(), month: current.getMonth() });
  }

  if (phase === "tomorrow") {
    return (
      <div className="overlay" data-wizard>
        <div className="wizard-modal wizard-tomorrow">
          <div className="wizard-body">
            <aside className="wizard-nav">
              <span className="wizard-date">{draftDate}</span>
              <span className="wizard-step current">[&gt;] tomorrow&apos;s goals</span>
              <span className="autosave-chip">{autosaved ? `autosaved ${autosaved}` : "not saved yet"}</span>
            </aside>
            <section className="wizard-pane">
              <h2>{date} closed <em>goals for {draftDate} · blank is fine</em></h2>
              {draftLoading && <p className="wizard-muted">loading tomorrow&apos;s goals…</p>}
              {draftLoadError && <><p className="wizard-error">{draftLoadError}</p><button className="wizard-button" onClick={() => setDraftLoadAttempt((attempt) => attempt + 1)} type="button">retry</button></>}
              {!draftLoading && !draftLoadError && <div className="wizard-goal-list">
                {draftGoals.map((goal, index) => (
                  <label className="wizard-line-input" key={index}>
                    <span>{index + 1}.</span>
                    <input autoFocus={index === 0} onChange={(event) => updateDraftGoal(index, event.target.value)} value={goal.text} />
                  </label>
                ))}
              </div>}
              {error && <p className="wizard-error">{error}</p>}
            </section>
          </div>
          <div className="wizard-footer">
            <Button className="wizard-button primary" disabled={saving || draftLoading || Boolean(draftLoadError)} onClick={finishDraft} type="button">Done</Button>
            <span className="wizard-hint"><kbd>esc</kbd> skip — goals can be filled in the morning</span>
          </div>
        </div>
      </div>
    );
  }

  const currentStep = WIZARD_STEPS[step];
  function renderStep() {
    if (loading) return <p className="wizard-muted">loading entry…</p>;
    if (entryLoadError) return <><p className="wizard-error">{entryLoadError}</p><button className="wizard-button" onClick={() => setEntryLoadAttempt((attempt) => attempt + 1)} type="button">retry</button></>;
    if (currentStep.id === "freeform") {
      return <textarea autoFocus className="wizard-freeform" onChange={(event) => updateEntry({ ...entry, text: event.target.value }, { text: event.target.value })} placeholder="How was your day?" value={entry.text} />;
    }
    if (currentStep.id === "day") {
      const wsFields = [
        ["went_well", "What went well today?"],
        ["could_have_gone_better", "What could have gone better?"],
        ["goal_for_tomorrow", "What are my goals for tomorrow?"],
      ];
      return (
        <div className="wizard-day">
          <section className="wizard-day-section">
            <h2>Today&apos;s goals <em>set last night — check off what happened</em></h2>
            <div className="wizard-goal-list">
              {entry.goals.map((goal, index) => (
                <div className="wizard-goal-row" key={index}>
                  <button aria-label={`${goal.checked ? "Uncheck" : "Check"} goal ${index + 1}`} className={`wizard-check ${goal.checked ? "checked" : ""}`} onClick={() => updateGoal(index, "checked", !goal.checked)} type="button">{goal.checked ? "[x]" : "[ ]"}</button>
                  {/* Goal text was written the night before — Tab runs the check buttons, so the inputs stay out of the tab order. */}
                  <input onChange={(event) => updateGoal(index, "text", event.target.value)} tabIndex={-1} value={goal.text} />
                </div>
              ))}
            </div>
          </section>
          <section className="wizard-day-section">
            <h2>Three gratitudes</h2>
            <div className="wizard-line-list">
              {entry.gratitudes.map((gratitude, index) => (
                <label className="wizard-line-input wizard-dashed-input" key={index}><span>-</span><input aria-label={`gratitude ${index + 1}`} onChange={(event) => updateGratitude(index, event.target.value)} value={gratitude} /></label>
              ))}
            </div>
          </section>
          <section className="wizard-day-section">
            <h2>The three Ws</h2>
            <div className="wizard-line-list">{wsFields.map(([field, label]) => <label className="wizard-line-input wizard-ws-input" key={field}><span>{label}</span><input onChange={(event) => updateWs(field, event.target.value)} value={entry.ws[field]} /></label>)}</div>
          </section>
        </div>
      );
    }
    if (currentStep.id === "ratings") {
      return (
        <div className="wizard-ratings-habits">
          <div className="wizard-ratings">
            {["total", "body", "mind", "spirit"].map((field) => <RatingSlider key={field} label={field[0].toUpperCase() + field.slice(1)} onChange={(value) => updateRating(field, value)} value={entry.ratings[field]} />)}
            <WorkHoursInput onChange={updateWorkHours} text={workHoursText} />
          </div>
          <button className="wizard-collapser" onClick={() => setHabitsOpen((open) => !open)} type="button">habits · {checkedHabits.size}/{activeHabits.length} {habitsOpen ? "[−]" : "[+]"}</button>
          {habitsOpen && <div className="wizard-habit-list">{activeHabits.map((habit) => {
            const checked = checkedHabits.has(String(habit.id));
            return <button className={`wizard-habit-row ${checked ? "checked" : ""}`} key={habit.id} onClick={() => toggleHabit(habit)} type="button"><span>{checked ? "[x]" : "[ ]"}</span><span>{habit.name}</span></button>;
          })}</div>}
          {habits.length > 0 && activeHabits.length === 0 && <p className="wizard-muted">no habits active on this date</p>}
        </div>
      );
    }
    return (
      <>
        <h2>Close {date === formatDate(new Date()) ? "the day" : date}</h2>
        <div className="wizard-save-note">everything is already autosaved — Save is the ritual close of the day.<br />{date === formatDate(new Date()) ? "closing today rolls straight into tomorrow's goals." : "re-saving a past day never re-triggers the next-day flow."}</div>
        <Button className="wizard-button primary wizard-save-button" disabled={saving} onClick={saveEntry} type="button">{saving ? "Saving…" : "Save — close the day"}</Button>
      </>
    );
  }

  return (
    <div className="overlay" data-wizard>
      <div className="wizard-modal">
        <div className="wizard-body">
          <aside className="wizard-nav">
            <div className="wizard-date-row">
              <EntryPixel rating={entry.ratings.total} />
              <button className="wizard-date-button" onClick={openPicker} type="button">{date}</button>
              <button
                aria-label={`Pixel marker: ${PIXEL_LABELS[entry.pixel] || "grey"}`}
                className={`wizard-pixel${entry.pixel ? ` wizard-pixel-${entry.pixel}` : ""}`}
                onClick={cyclePixel}
                title={`Pixel marker: ${PIXEL_LABELS[entry.pixel] || "grey"}`}
                type="button"
              />
            </div>
            {WIZARD_STEPS.map((wizardStep, index) => <button className={`wizard-step ${index === step ? "current" : ""}`} key={wizardStep.id} onClick={() => setStep(index)} type="button">[{index === step ? ">" : filled[wizardStep.id] ? "x" : " "}] {wizardStep.label}</button>)}
            <span className="autosave-chip">{autosaved ? `autosaved ${autosaved}` : "not saved yet"}</span>
            {picker && <MonthPicker entryDates={entryDates} onClose={() => setPicker(null)} onNavigate={setPicker} onSelect={selectDate} picker={picker} selectedDate={date} />}
          </aside>
          <section className="wizard-pane" onKeyDown={onPaneKeyDown} ref={paneRef}>
            {currentStep.title && <h2>{currentStep.title} {currentStep.hint && <em>{currentStep.hint}</em>}</h2>}
            {renderStep()}
            {error && <p className="wizard-error">{error}</p>}
          </section>
        </div>
        <div className="wizard-footer">
          {step > 0 && <button className="wizard-button" onClick={() => setStep((current) => current - 1)} type="button">← Back</button>}
          {step < WIZARD_STEPS.length - 1 && <Button className="wizard-button primary" onClick={() => setStep((current) => current + 1)} type="button">Next →</Button>}
          <span className="wizard-hint">click any step to jump · click the date to backfill · <kbd>esc</kbd> close</span>
        </div>
      </div>
    </div>
  );
}

function Header({ page, navigate, setup = false }) {
  return (
    <header className={`site-header${setup ? " setup-header" : ""}`}>
      <div className="header-inner">
        {setup ? <span className="wordmark"><span>∆</span> DELTA</span> : (
          <button className="wordmark" type="button" onClick={() => navigate("grid")}>
            <span>∆</span> DELTA
          </button>
        )}
        {setup ? <span className="setup-sub">first run · setup mode</span> : (
          <nav aria-label="Main navigation">
            {NAV_ITEMS.map((item) => (
              <button
                className={page === item.id ? "active" : ""}
                type="button"
                key={item.id}
                aria-current={page === item.id ? "page" : undefined}
                onClick={() => navigate(item.id)}
              >
                {item.label}
              </button>
            ))}
          </nav>
        )}
      </div>
    </header>
  );
}

function YearRail({ year, setYear, years = [year] }) {
  return (
    <aside className="year-rail" aria-label="Year selection">
      <span className="frame-title">year</span>
      {[...years].sort((a, b) => b - a).map((item) => (
        <button className={item === year ? "on" : ""} key={item} onClick={() => setYear(item)} type="button">
          {item}
        </button>
      ))}
    </aside>
  );
}

function pixelClass(day, view) {
  if (!day?.has_entry) return undefined;
  if (view === "rating") {
    return ratingRampClass(day.rating) ?? journalOnlyClass(day);
  }
  if (view === "habit" && day.habit_score == null) return journalOnlyClass(day);
  return undefined;
}

// A day carrying a journal but no metric for the current view still reads as
// written-on, so it lifts off the empty base instead of looking untouched.
function journalOnlyClass(day) {
  return day.journal ? "px-journal" : undefined;
}

function ratingRampClass(value) {
  if (value == null) return undefined;
  const rating = Math.max(1, Math.min(5, Math.round(value)));
  return `px-r${rating}`;
}

// The day-rating pixel beside the date in the wizard and the read popup. It
// shares the grid's px-r* ramp so an entry reads the same colour everywhere,
// and always renders — an unrated day just keeps the empty pixel colour.
function EntryPixel({ rating }) {
  const ramp = ratingRampClass(rating);
  return <span aria-hidden="true" className={`entry-pixel${ramp ? ` ${ramp}` : ""}`} />;
}

function continuousRampColor(percent) {
  const clamped = Math.max(0, Math.min(100, percent));
  return `hsl(${clamped * 1.45}, 85%, 55%)`;
}

function pixelColor(day, view) {
  if (view === "habit" && day?.has_entry && day.habit_score != null) {
    return continuousRampColor(day.habit_score);
  }
  return undefined;
}

function markerColor(day) {
  return PIXEL_COLORS[day?.pixel] || undefined;
}

function formatMetric(value, suffix = "") {
  return value == null ? "—" : `${value}${suffix}`;
}

function DayTooltip({ day, position }) {
  if (!day) return null;
  // A future date has no metrics to report, so the tooltip is a date preview.
  if (day.future) {
    return (
      <div className="day-tooltip" role="tooltip" style={position}>
        <strong>{day.date}</strong>
        <span>not yet</span>
      </div>
    );
  }
  const habit = day.has_entry && day.habit_score != null ? `${Math.round(day.habit_score)}%` : day.has_entry ? "—" : "0%";
  return (
    <div className="day-tooltip" role="tooltip" style={position}>
      <strong>{day.date}</strong>
      <span>total {formatMetric(day.rating)} · body {formatMetric(day.body)} · mind {formatMetric(day.mind)} · spirit {formatMetric(day.spirit)}</span>
      <span>habits: {habit} · journal: {day.journal ? "yes" : "no"}</span>
    </div>
  );
}

// Weekday-label column width and pixel gap in real pixels: cells are sized to
// whole pixels from the measured width so every gap renders identically
// instead of drifting with fractional column rounding.
const GRID_WEEKDAY_WIDTH = 26;
const GRID_GAP = 2;

function PixelGrid({ year, view, days = [], marker = false, onOpen }) {
  const { cells, monthMarks, weeks } = useMemo(() => calendarFor(year), [year]);
  const today = formatDate(new Date());
  const [measureRef, width] = useMeasuredWidth();
  const cellSize = width > 0 ? Math.max(4, Math.floor((width - GRID_WEEKDAY_WIDTH - weeks * GRID_GAP) / weeks)) : 0;
  const template = { gridTemplateColumns: `${GRID_WEEKDAY_WIDTH}px repeat(${weeks}, ${cellSize}px)` };
  const daysByDate = useMemo(() => new Map(days.map((day) => [day.date, day])), [days]);
  const gridBoxRef = useRef(null);
  const [hovered, setHovered] = useState(null);

  function showTooltip(day, event) {
    if (!day || !gridBoxRef.current) {
      setHovered(null);
      return;
    }
    const cell = event.currentTarget.getBoundingClientRect();
    const gridBox = gridBoxRef.current.getBoundingClientRect();
    setHovered({
      day,
      position: {
        left: `${Math.max(0, cell.left - gridBox.left)}px`,
        top: `${cell.bottom - gridBox.top + 6}px`,
      },
    });
  }

  return (
    <div
      className="grid-box"
      ref={(node) => {
        gridBoxRef.current = node;
        measureRef.current = node;
      }}
      aria-label={`${year} diary grid`}
    >
      {cellSize > 0 && (
      <>
      <div className="month-row" style={template}>
        <span />
        {monthMarks.map(({ month, week }) => (
          <span key={month} style={{ gridColumn: week + 2 }}>
            {MONTHS[month]}
          </span>
        ))}
      </div>
      <div className="pixels" style={template}>
        {WEEKDAYS.map((label, index) => (
          <span className="weekday" key={index}>
            {label}
          </span>
        ))}
        {cells.map(({ inYear, key }) => {
          const day = daysByDate.get(key);
          const tooltipDay = inYear ? (key > today ? { date: key, future: true } : day) : null;
          const color = marker ? markerColor(day) : pixelColor(day, view);
          const className = marker ? undefined : pixelClass(day, view);
          return (
          <button
            aria-label={inYear ? key : "outside selected year"}
            className={`pixel${inYear ? "" : " void"}${key === today ? " today" : ""}${className ? ` ${className}` : ""}`}
            onFocus={(event) => showTooltip(tooltipDay, event)}
            onMouseEnter={(event) => showTooltip(tooltipDay, event)}
            onMouseLeave={() => setHovered(null)}
            onBlur={() => setHovered(null)}
            onClick={() => {
              if (inYear) onOpen(key, Boolean(day?.has_entry));
            }}
            style={color ? { backgroundColor: color } : undefined}
            key={key}
            type="button"
          />
          );
        })}
      </div>
      <DayTooltip day={hovered?.day} position={hovered?.position} />
      </>
      )}
    </div>
  );
}

function HighlightedSnippet({ snippet }) {
  const parts = String(snippet || "").split(/(<mark>|<\/mark>)/g);
  let highlighted = false;
  function decodeText(value) {
    return value
      .replaceAll("&lt;mark&gt;", "<mark>")
      .replaceAll("&lt;/mark&gt;", "</mark>");
  }
  return (
    <span className="search-result-snippet">
      {parts.map((part, index) => {
        if (part === "<mark>") {
          highlighted = true;
          return null;
        }
        if (part === "</mark>") {
          highlighted = false;
          return null;
        }
        return <span className={highlighted ? "search-highlight" : ""} key={index}>{decodeText(part)}</span>;
      })}
    </span>
  );
}

function SearchDropdown({ onOpen, inputRef }) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState([]);
  const [open, setOpen] = useState(false);
  const [error, setError] = useState("");
  const searchRef = useRef(null);

  useEffect(() => {
    const trimmed = query.trim();
    if (!trimmed) {
      setResults([]);
      setError("");
      return undefined;
    }
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      apiRequest(`/api/search?q=${encodeURIComponent(query)}`, { signal: controller.signal }, {
        fallbackMessage: "search request failed",
        fallbackCode: "search_error",
      })
        .then((body) => {
          setResults(Array.isArray(body) ? body : []);
          setError("");
          setOpen(true);
        })
        .catch((requestError) => {
          if (requestError.name !== "AbortError") {
            setResults([]);
            setError(requestError.message);
            setOpen(true);
          }
        });
    }, 250);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [query]);

  useEffect(() => {
    function closeOnOutsideClick(event) {
      if (!searchRef.current?.contains(event.target)) setOpen(false);
    }
    function closeOnEscape(event) {
      if (event.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", closeOnOutsideClick);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("mousedown", closeOnOutsideClick);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, []);

  return (
    <div className="search-box" ref={searchRef}>
      <span className="search-glyph">/</span>
      <input
        aria-label="Search entries"
        onChange={(event) => {
          setQuery(event.target.value);
          setOpen(Boolean(event.target.value.trim()));
        }}
        onFocus={() => {
          if (query.trim()) setOpen(true);
        }}
        placeholder="Search entries"
        ref={inputRef}
        value={query}
      />
      {open && (
        <div aria-label="Search results" className="search-results" role="listbox">
          {error && <p className="search-result-error">{error}</p>}
          {!error && results.length === 0 && <p className="search-result-empty">no matches</p>}
          {!error && results.map((result, index) => (
            <button
              className="search-result"
              key={`${result.date}-${result.field}-${index}`}
              onClick={() => {
                setOpen(false);
                onOpen(result.date, true);
              }}
              type="button"
            >
              <span className="search-result-meta">{result.date} · {result.field} ·</span>
              <HighlightedSnippet snippet={result.snippet} />
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// The grid data, the view toggle and the marker overlay all live in App: the
// status bar needs the summary on every page and the shortcuts must reach the
// view toggle while this page is unmounted. Every recorded year renders as
// its own grid, newest on top; only the App-year grid comes preloaded, the
// rest are fetched here.
function GridPage({ year, onOpen, grid, error, view, setView, marker, searchInputRef, gridRefresh }) {
  const years = useMemo(() => [...(grid?.years || [year])].sort((a, b) => b - a), [grid, year]);
  const yearsKey = years.join(",");
  const [extraDays, setExtraDays] = useState({});
  const [extraError, setExtraError] = useState("");

  useEffect(() => {
    const others = years.filter((item) => item !== year);
    if (others.length === 0) return undefined;
    const controller = new AbortController();
    Promise.all(others.map((item) =>
      apiRequest(`/api/grid?year=${item}`, { signal: controller.signal }, {
        fallbackMessage: "grid request failed",
        fallbackCode: "grid_error",
      })
        .then((body) => [item, body?.days || []])
        .catch((requestError) => {
          if (requestError.name !== "AbortError") setExtraError(requestError.message);
          return [item, []];
        })
    )).then((loaded) => setExtraDays(Object.fromEntries(loaded)));
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [yearsKey, year, gridRefresh]);

  return (
    <>
      <div className="toolbar">
        <SearchDropdown inputRef={searchInputRef} onOpen={onOpen} />
        <div className="segmented" role="group" aria-label="Grid view">
          <button className={view === "rating" ? "on" : ""} onClick={() => setView("rating")} type="button">
            Rating
          </button>
          <button className={view === "habit" ? "on" : ""} onClick={() => setView("habit")} type="button">
            Habits
          </button>
        </div>
        <Button className="new-entry" onClick={() => onOpen(formatDate(new Date()))} type="button">
          + New entry
        </Button>
      </div>
      <main className="main-column">
        {error && <p className="grid-error">{error}</p>}
        {extraError && <p className="grid-error">{extraError}</p>}
        {years.map((item) => (
          <section className="year-grid" key={item}>
            <h2 className="year-grid-label">{item}</h2>
            <PixelGrid
              days={item === year ? grid?.days : extraDays[item]}
              marker={marker}
              onOpen={onOpen}
              view={view}
              year={item}
            />
          </section>
        ))}
      </main>
    </>
  );
}

function statColor(value, min = 0, max = 100) {
  if (value == null) return undefined;
  const percent = Math.max(0, Math.min(100, ((value - min) / (max - min)) * 100));
  return continuousRampColor(percent);
}

function StatsBar({ label, value, min = 0, max = 100, display, fillClass }) {
  const width = value == null ? 0 : Math.max(0, Math.min(100, ((value - min) / (max - min)) * 100));
  const fillStyle = { width: `${width}%` };
  if (!fillClass) fillStyle.backgroundColor = statColor(value, min, max);
  return (
    <div className="stats-bar">
      <span className="stats-bar-label">{label}</span>
      <span className="stats-bar-track">
        <span className={`stats-bar-fill${fillClass ? ` ${fillClass}` : ""}`} style={fillStyle} />
      </span>
      <span className="stats-bar-value">{display ?? (value == null ? "—" : value.toFixed(1))}</span>
    </div>
  );
}

function monthLabel(month) {
  const index = Number(month?.slice(5, 7)) - 1;
  return MONTHS[index] || month;
}

function formatRating(value) {
  return value.toFixed(1);
}

function formatPercent(value) {
  return `${Math.round(value)}%`;
}

function formatHours(value) {
  return `${value.toFixed(1)}h`;
}

// Line-graph geometry in real pixels. The SVG is drawn 1:1 against its measured
// width rather than scaled from a viewBox, so its <text> keeps the universal
// body font size instead of being stretched by the column width.
const CHART_HEIGHT = 140;
const CHART_TOP = 12;
const CHART_BOTTOM = 116;
// Wide enough for the longest tick label ("100%") at the universal 12px mono.
const CHART_GUTTER = 40;
const CHART_RIGHT_PAD = 8;

function useMeasuredWidth() {
  const ref = useRef(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const node = ref.current;
    if (!node) return undefined;
    const observer = new ResizeObserver(([observed]) => setWidth(Math.round(observed.contentRect.width)));
    observer.observe(node);
    setWidth(Math.round(node.getBoundingClientRect().width));
    return () => observer.disconnect();
  }, []);
  return [ref, width];
}

function MonthLineChart({ label, points, format }) {
  const [ref, width] = useMeasuredWidth();
  const [hoveredPoint, setHoveredPoint] = useState(null);
  const recorded = points.map((point, index) => ({ ...point, index })).filter((point) => point.value != null);
  const values = recorded.map((point) => point.value);
  const low = values.length ? Math.min(...values) : 0;
  const high = values.length ? Math.max(...values) : 0;
  const span = high - low;
  const plotWidth = Math.max(0, width - CHART_GUTTER - CHART_RIGHT_PAD);
  const middle = (CHART_TOP + CHART_BOTTOM) / 2;
  const xAt = (index) => (points.length > 1 ? CHART_GUTTER + (plotWidth * index) / (points.length - 1) : CHART_GUTTER + plotWidth / 2);
  // Each chart derives its own scale from its own values; a flat series has no
  // span to divide by and simply rides the middle of the plot.
  const yAt = (value) => (span === 0 ? middle : CHART_BOTTOM - ((value - low) / span) * (CHART_BOTTOM - CHART_TOP));

  // Consecutive recorded months form one path each, so a month with no data
  // breaks the line rather than being drawn as a zero.
  const segments = [];
  let run = [];
  points.forEach((point, index) => {
    if (point.value == null) {
      if (run.length) segments.push(run);
      run = [];
      return;
    }
    run.push({ ...point, index });
  });
  if (run.length) segments.push(run);

  return (
    <div className="stats-line-chart" ref={ref}>
      {width > 0 && (
        <svg aria-label={label} height={CHART_HEIGHT} role="img" width={width}>
          <line className="stats-line-grid" x1={CHART_GUTTER} x2={CHART_GUTTER + plotWidth} y1={CHART_TOP} y2={CHART_TOP} />
          <line className="stats-line-grid" x1={CHART_GUTTER} x2={CHART_GUTTER + plotWidth} y1={CHART_BOTTOM} y2={CHART_BOTTOM} />
          {values.length > 0 && (span === 0 ? (
            <text className="stats-line-tick" dominantBaseline="middle" textAnchor="end" x={CHART_GUTTER - 8} y={middle}>{format(high)}</text>
          ) : (
            <>
              <text className="stats-line-tick" dominantBaseline="middle" textAnchor="end" x={CHART_GUTTER - 8} y={CHART_TOP}>{format(high)}</text>
              <text className="stats-line-tick" dominantBaseline="middle" textAnchor="end" x={CHART_GUTTER - 8} y={CHART_BOTTOM}>{format(low)}</text>
            </>
          ))}
          {values.length === 0 && (
            <text className="stats-line-empty" dominantBaseline="middle" textAnchor="middle" x={CHART_GUTTER + plotWidth / 2} y={middle}>no data</text>
          )}
          {segments.map((segment) => segment.length > 1 && (
            <path
              className="stats-line-path"
              d={segment.map((point, order) => `${order === 0 ? "M" : "L"}${xAt(point.index).toFixed(1)} ${yAt(point.value).toFixed(1)}`).join(" ")}
              key={segment[0].month}
            />
          ))}
          {recorded.map((point) => (
            <g
              key={point.month}
              onMouseEnter={() => setHoveredPoint(point)}
              onMouseLeave={() => setHoveredPoint(null)}
            >
              <circle className="stats-line-dot" cx={xAt(point.index)} cy={yAt(point.value)} r={hoveredPoint?.month === point.month ? 3 : 2} />
              <circle className="stats-line-hit" cx={xAt(point.index)} cy={yAt(point.value)} r="8" />
            </g>
          ))}
          {points.map((point, index) => (
            <text className="stats-line-month" key={point.month} textAnchor="middle" x={xAt(index)} y={CHART_HEIGHT - 6}>{monthLabel(point.month).slice(0, 1)}</text>
          ))}
          {hoveredPoint && (() => {
            // The hovered value is drawn as an in-SVG tooltip: mono glyphs are
            // ~7.2px wide at 12px, and the box is clamped to the plot area
            // and flipped below the dot when it would clip the top edge.
            const tooltipText = `${monthLabel(hoveredPoint.month)} · ${format(hoveredPoint.value)}`;
            const boxWidth = tooltipText.length * 7.2 + 14;
            const dotX = xAt(hoveredPoint.index);
            const dotY = yAt(hoveredPoint.value);
            const boxX = Math.max(CHART_GUTTER, Math.min(dotX - boxWidth / 2, CHART_GUTTER + plotWidth - boxWidth));
            const boxY = dotY - 28 >= 0 ? dotY - 28 : dotY + 10;
            return (
              <g className="stats-line-tooltip" pointerEvents="none">
                <rect height="20" width={boxWidth} x={boxX} y={boxY} />
                <text dominantBaseline="middle" textAnchor="middle" x={boxX + boxWidth / 2} y={boxY + 11}>{tooltipText}</text>
              </g>
            );
          })()}
        </svg>
      )}
    </div>
  );
}

function StatsViewToggle({ view, setView }) {
  return (
    <div aria-label="Monthly averages view" className="stats-view-toggle" role="group">
      <button aria-label="Table view" aria-pressed={view === "table"} className={view === "table" ? "active" : ""} onClick={() => setView("table")} title="table" type="button">
        <svg aria-hidden="true" height="14" viewBox="0 0 14 14" width="14">
          <path d="M1.5 2.5h11v9h-11z" />
          <path d="M1.5 5.5h11" />
          <path d="M5.5 5.5v6" />
        </svg>
      </button>
      <button aria-label="Line graph view" aria-pressed={view === "graph"} className={view === "graph" ? "active" : ""} onClick={() => setView("graph")} title="graph" type="button">
        <svg aria-hidden="true" height="14" viewBox="0 0 14 14" width="14">
          <path d="M1.5 1.5v11h11" />
          <path d="M3.5 9.5l2.5-2.5 2 1.5 3.5-4.5" />
        </svg>
      </button>
    </div>
  );
}

function StatsPage({ year, setYear }) {
  const [stats, setStats] = useState(null);
  const [error, setError] = useState("");
  const [chartView, setChartView] = useState("table");

  useEffect(() => {
    const controller = new AbortController();
    const query = new URLSearchParams({ from: `${year}-01-01`, to: `${year}-12-31`, agg: "month" });
    setError("");
    apiFetch(`/api/stats?${query.toString()}`, { signal: controller.signal })
      .then(async (response) => {
        const body = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(body.error?.message || "stats request failed");
        return body;
      })
      .then((body) => setStats(body))
      .catch((requestError) => {
        if (requestError.name !== "AbortError") {
          setStats(null);
          setError(requestError.message);
        }
      });
    return () => controller.abort();
  }, [year]);

  const years = stats?.years || [year];
  const streaks = stats?.streaks || [];
  const completion = stats?.completion || [];
  const blankMonths = Array.from({ length: 12 }, (_, index) => ({ month: `${year}-${String(index + 1).padStart(2, "0")}` }));
  const rating = stats?.rating || blankMonths;
  const habitScore = stats?.habit_score || blankMonths;
  const workHours = stats?.work_hours || blankMonths;
  const totalAverage = stats?.averages?.total;
  const habitAverage = stats?.averages?.habit_score;
  const workAverage = stats?.averages?.work_hours;

  return (
    <>
      <div className="rail-wrap on-top-row">
        <YearRail setYear={setYear} year={year} years={years} />
      </div>
      <main className="main-column on-top-row stats-page">
        <div className="stats-heading">
          <span className="section-label">stats</span>
          <span className="placeholder-copy">monthly signals · {year}</span>
        </div>
        {error && <p className="grid-error">{error}</p>}
        <div className="stats-tiles">
          <section className="stats-tile">
            <span className="section-label">habit streaks</span>
            <div className="stats-streak-list">
              {streaks.length === 0 ? <span className="stats-empty">—</span> : streaks.map((streak) => (
                <div className="stats-detail-row" key={streak.id}>
                  <span>{streak.name}</span><strong>{streak.current} / {streak.best}</strong>
                </div>
              ))}
            </div>
          </section>
          <section className="stats-tile">
            <span className="section-label">characters in {year}</span>
            <span className="stats-value">{(stats?.characters ?? 0).toLocaleString()}</span>
            <span className="stats-caption">freeform prose</span>
          </section>
          <section className="stats-tile">
            <span className="section-label">averages · {year}</span>
            <div className="stats-detail-row"><span>Total rating</span><strong>{totalAverage == null ? "—" : totalAverage.toFixed(2)}</strong></div>
            <div className="stats-detail-row"><span>Habit score</span><strong>{habitAverage == null ? "—" : formatPercent(habitAverage)}</strong></div>
            <div className="stats-detail-row"><span>Work hours</span><strong>{workAverage == null ? "—" : formatHours(workAverage)}</strong></div>
          </section>
        </div>
        <section className="stats-section">
          <h2>habit completion · {year}</h2>
          <div className="stats-bars">
            {completion.length === 0 ? <span className="stats-empty">no habits</span> : completion.map((habit) => (
              <StatsBar key={habit.id} label={habit.name} value={habit.percent} display={`${Math.round(habit.percent)}%`} />
            ))}
          </div>
        </section>
        <div className="stats-chart-toolbar">
          <StatsViewToggle setView={setChartView} view={chartView} />
        </div>
        <div className="stats-chart-grid">
          <section className="stats-section">
            <h2>average Total · {year}</h2>
            {chartView === "graph" ? <MonthLineChart format={formatRating} label={`average Total by month, ${year}`} points={rating} /> : (
              <div className="stats-bars">
                {rating.map((point) => <StatsBar key={point.month} label={monthLabel(point.month)} min={1} max={5} value={point.value} fillClass={ratingRampClass(point.value)} />)}
              </div>
            )}
          </section>
          <section className="stats-section">
            <h2>average habit score · {year}</h2>
            {chartView === "graph" ? <MonthLineChart format={formatPercent} label={`average habit score by month, ${year}`} points={habitScore} /> : (
              <div className="stats-bars">
                {habitScore.map((point) => <StatsBar key={point.month} label={monthLabel(point.month)} value={point.value} display={point.value == null ? "—" : formatPercent(point.value)} />)}
              </div>
            )}
          </section>
          <section className="stats-section">
            <h2>average work hours · {year}</h2>
            {chartView === "graph" ? <MonthLineChart format={formatHours} label={`average work hours by month, ${year}`} points={workHours} /> : (
              <div className="stats-bars">
                {/* Work hours carry no good/bad direction, so the bars stay accent
                    rather than borrowing the rating ramp's red-to-green reading. */}
                {workHours.map((point) => <StatsBar key={point.month} fillClass="stats-bar-fill-accent" label={monthLabel(point.month)} max={24} value={point.value} display={point.value == null ? "—" : formatHours(point.value)} />)}
              </div>
            )}
          </section>
        </div>
      </main>
    </>
  );
}

const DROP_END = "end";

function isHabitActiveOnDate(habit, date) {
  return (habit.ranges || []).some((range) => (
    range.active_from <= date && (range.active_to == null || range.active_to >= date)
  ));
}

function isHabitActiveToday(habit) {
  const today = formatDate(new Date());
  // Date derivation counts a range ending today, but Settings needs to show
  // the API's closed-today pause marker in the Inactive zone for same-day undo.
  return isHabitActiveOnDate(habit, today) && !(habit.ranges || []).some((range) => range.active_to === today);
}

function cloneHabitRanges(habit) {
  return (habit.ranges || []).map((range) => ({
    active_from: range.active_from,
    active_to: range.active_to || "",
  }));
}

function formatHabitRanges(habit) {
  return (habit.ranges || []).map((range) => `${range.active_from} → ${range.active_to || "…"}`).join(" · ");
}

function SettingsPage({ navigate, section, onActiveHabitCountChange, onLastBackupChange }) {
  const [habits, setHabits] = useState([]);
  const [newHabitName, setNewHabitName] = useState("");
  const [editingHabitId, setEditingHabitId] = useState(null);
  const [renameDraft, setRenameDraft] = useState("");
  const [expandedHabitId, setExpandedHabitId] = useState(null);
  const [rangeDrafts, setRangeDrafts] = useState({});
  const [draggedHabitId, setDraggedHabitId] = useState(null);
  const [dropTarget, setDropTarget] = useState(null);
  const [inactiveDropReady, setInactiveDropReady] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState(null);
  const [settingsInfo, setSettingsInfo] = useState(null);
  const [settingsLoading, setSettingsLoading] = useState(true);
  const [revealedSecrets, setRevealedSecrets] = useState({ key: null, token: null });
  const [editingKey, setEditingKey] = useState(false);
  const [keyDraft, setKeyDraft] = useState("");
  const [settingsMessage, setSettingsMessage] = useState("");
  const [confirmRegenerate, setConfirmRegenerate] = useState(false);

  function setHabitList(nextHabits) {
    const list = Array.isArray(nextHabits) ? nextHabits : [];
    setHabits(list);
    const today = formatDate(new Date());
    // The backend keeps a habit active through its pause day (active_to >= today).
    onActiveHabitCountChange(list.filter((habit) => isHabitActiveOnDate(habit, today)).length);
  }

  async function refreshHabits(signal) {
    const nextHabits = await apiRequest("/api/habits", signal ? { signal } : {});
    setHabitList(nextHabits);
    return nextHabits;
  }

  async function refreshSettings(signal) {
    const nextSettings = await apiRequest("/api/settings", signal ? { signal } : {});
    setSettingsInfo(nextSettings);
    onLastBackupChange(nextSettings.last_backup || "");
    return nextSettings;
  }

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setSettingsLoading(true);
    setError(null);
    setRevealedSecrets({ key: null, token: null });
    setEditingKey(false);
    setKeyDraft("");
    setConfirmRegenerate(false);
    // Per-action feedback belongs to the tab that produced it; leaving the tab
    // (or the page, which unmounts this component) drops it.
    setSettingsMessage("");
    Promise.all([refreshHabits(controller.signal), refreshSettings(controller.signal)])
      .catch((requestError) => {
        if (requestError.name !== "AbortError") setError(requestError);
      })
      .finally(() => {
        setLoading(false);
        setSettingsLoading(false);
      });
    return () => controller.abort();
  }, [section]);

  function selectSection(event, nextSection) {
    event.preventDefault();
    navigate(nextSection);
  }

  async function finishMutation(action) {
    setSaving(true);
    setError(null);
    try {
      await action();
    } catch (requestError) {
      setError(requestError);
    } finally {
      try {
        await refreshHabits();
      } catch (requestError) {
        if (requestError.name !== "AbortError") setError(requestError);
      }
      setSaving(false);
    }
  }

  function patchHabit(id, payload) {
    return apiRequest(`/api/habits/${id}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
  }

  async function fetchSecret(kind) {
    const nextSettings = await apiRequest(`/api/settings?reveal=${kind}`);
    return kind === "key" ? nextSettings.key : nextSettings.token;
  }

  async function revealSecret(kind) {
    const value = await fetchSecret(kind);
    setRevealedSecrets((current) => ({ ...current, [kind]: value }));
    return value;
  }

  function copySecret(kind) {
    setError(null);
    return fetchSecret(kind);
  }

  async function toggleSecret(kind) {
    if (revealedSecrets[kind]) {
      setRevealedSecrets((current) => ({ ...current, [kind]: null }));
      return;
    }
    setError(null);
    try {
      await revealSecret(kind);
    } catch (requestError) {
      setError(requestError);
    }
  }

  async function startKeyEdit() {
    setError(null);
    try {
      const value = await revealSecret("key");
      setKeyDraft(value);
      setEditingKey(true);
    } catch (requestError) {
      setError(requestError);
    }
  }

  async function saveKey() {
    if (saving) return;
    setSaving(true);
    setError(null);
    setSettingsMessage("");
    try {
      const response = await apiRequest("/api/settings", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ key: keyDraft }),
      });
      setSettingsInfo(response.settings);
      onLastBackupChange(response.settings?.last_backup || "");
      setRevealedSecrets((current) => ({ ...current, key: null }));
      setEditingKey(false);
      setKeyDraft("");
      setSettingsMessage("saved — restart DELTA to open with the new key");
    } catch (requestError) {
      setError(requestError);
    } finally {
      setSaving(false);
    }
  }

  function cancelKeyEdit() {
    setEditingKey(false);
    setKeyDraft("");
    setRevealedSecrets((current) => ({ ...current, key: null }));
  }

  async function regenerateToken() {
    if (saving) return;
    if (!confirmRegenerate) {
      setConfirmRegenerate(true);
      return;
    }
    setConfirmRegenerate(false);
    setSaving(true);
    setError(null);
    setSettingsMessage("");
    try {
      const response = await apiRequest("/api/settings/token/regenerate", { method: "POST" });
      window.__DELTA_TOKEN__ = response.token;
      setSettingsInfo(response.settings);
      setRevealedSecrets((current) => ({ ...current, token: response.token }));
      onLastBackupChange(response.settings?.last_backup || "");
      setSettingsMessage("regenerated — existing clients must use the new token");
    } catch (requestError) {
      setError(requestError);
    } finally {
      setSaving(false);
    }
  }

  async function backupNow() {
    if (saving) return;
    setSaving(true);
    setError(null);
    setSettingsMessage("");
    try {
      await apiRequest("/api/backup", { method: "POST" });
      const nextSettings = await refreshSettings();
      onLastBackupChange(nextSettings.last_backup || "");
      setSettingsMessage("snapshot written");
    } catch (requestError) {
      setError(requestError);
    } finally {
      setSaving(false);
    }
  }

  function startRename(habit) {
    setEditingHabitId(habit.id);
    setRenameDraft(habit.name);
    setError(null);
  }

  function cancelRename() {
    setEditingHabitId(null);
    setRenameDraft("");
  }

  function commitRename(habit) {
    if (editingHabitId !== habit.id) return;
    const name = renameDraft.trim();
    if (!name) {
      setError({ code: "invalid_habit", message: "habit name cannot be empty" });
      return;
    }
    if (name === habit.name) {
      cancelRename();
      return;
    }
    void finishMutation(async () => {
      await patchHabit(habit.id, { name });
      cancelRename();
    });
  }

  async function addHabit() {
    const name = newHabitName.trim();
    if (!name || saving) return;
    setNewHabitName("");
    await finishMutation(async () => {
      const created = await apiRequest("/api/habits", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      });
      await patchHabit(created.id, { position: 0 });
    });
  }

  function toggleRanges(habit) {
    if (expandedHabitId === habit.id) {
      setExpandedHabitId(null);
      return;
    }
    setExpandedHabitId(habit.id);
    setRangeDrafts((current) => ({ ...current, [habit.id]: cloneHabitRanges(habit) }));
    setError(null);
  }

  function updateRange(habitId, index, field, value) {
    setRangeDrafts((current) => ({
      ...current,
      [habitId]: (current[habitId] || []).map((range, rangeIndex) => (
        rangeIndex === index ? { ...range, [field]: value } : range
      )),
    }));
  }

  function addRange(habitId) {
    setRangeDrafts((current) => ({
      ...current,
      [habitId]: [...(current[habitId] || []), { active_from: formatDate(new Date()), active_to: "" }],
    }));
  }

  function removeRange(habitId, index) {
    setRangeDrafts((current) => ({
      ...current,
      [habitId]: (current[habitId] || []).filter((_, rangeIndex) => rangeIndex !== index),
    }));
  }

  async function saveRanges(habit) {
    if (saving) return;
    const ranges = (rangeDrafts[habit.id] || []).map((range) => ({
      active_from: range.active_from,
      active_to: range.active_to || null,
    }));
    await finishMutation(async () => {
      const updated = await patchHabit(habit.id, { ranges });
      setRangeDrafts((current) => ({ ...current, [habit.id]: cloneHabitRanges(updated) }));
      setExpandedHabitId(null);
    });
  }

  function startDrag(event, habit) {
    if (saving || editingHabitId === habit.id) {
      event.preventDefault();
      return;
    }
    setDraggedHabitId(habit.id);
    setDropTarget(null);
    setInactiveDropReady(false);
    setError(null);
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", String(habit.id));
  }

  function finishDrag() {
    setDraggedHabitId(null);
    setDropTarget(null);
    setInactiveDropReady(false);
  }

  function handleActiveRowDragOver(event, targetId) {
    if (draggedHabitId == null || saving) return;
    event.preventDefault();
    event.stopPropagation();
    setInactiveDropReady(false);
    if (targetId === draggedHabitId) {
      setDropTarget(null);
      return;
    }
    const activeHabits = habits.filter(isHabitActiveToday);
    const available = activeHabits.filter((habit) => habit.id !== draggedHabitId);
    const targetIndex = available.findIndex((habit) => habit.id === targetId);
    if (targetIndex < 0) return;
    const before = event.clientY < event.currentTarget.getBoundingClientRect().top + event.currentTarget.getBoundingClientRect().height / 2;
    setDropTarget(before ? targetId : (available[targetIndex + 1]?.id ?? DROP_END));
  }

  function handleActiveDragOver(event) {
    if (draggedHabitId == null || saving) return;
    event.preventDefault();
    const row = event.target.closest?.("[data-habit-row]");
    if (!row) {
      setInactiveDropReady(false);
      setDropTarget(DROP_END);
    }
  }

  async function dropIntoActive(event) {
    event.preventDefault();
    event.stopPropagation();
    if (draggedHabitId == null || dropTarget == null || saving) return;
    const id = draggedHabitId;
    const habit = habits.find((item) => item.id === id);
    const activeHabits = habits.filter(isHabitActiveToday);
    const available = activeHabits.filter((item) => item.id !== id);
    const target = dropTarget === DROP_END ? null : available.find((item) => item.id === dropTarget);
    if (!habit || (dropTarget !== DROP_END && !target)) {
      finishDrag();
      return;
    }
    // PATCH positions cover inactive habits too, so anchor the insertion at
    // the target's global position rather than its filtered active index.
    const requestedPosition = target
      ? (habit.position < target.position ? target.position - 1 : target.position)
      : habits.length - 1;
    const position = Math.max(0, Math.min(requestedPosition, habits.length - 1));
    const wasActive = isHabitActiveToday(habit);
    finishDrag();
    if (wasActive && position === habit.position) return;
    await finishMutation(async () => {
      if (!wasActive) {
        await patchHabit(id, { archived: false });
      }
      await patchHabit(id, { position });
    });
  }

  async function dropIntoInactive(event) {
    event.preventDefault();
    event.stopPropagation();
    const habit = habits.find((item) => item.id === draggedHabitId);
    if (!habit || !isHabitActiveToday(habit) || saving) {
      finishDrag();
      return;
    }
    const id = draggedHabitId;
    finishDrag();
    await finishMutation(() => patchHabit(id, { archived: true }));
  }

  const activeHabits = habits.filter(isHabitActiveToday);
  const inactiveHabits = habits.filter((habit) => !isHabitActiveToday(habit));
  const draggedHabit = habits.find((habit) => habit.id === draggedHabitId);

  function renderHabitRow(habit, index, active) {
    const editing = editingHabitId === habit.id;
    const expanded = expandedHabitId === habit.id;
    const ranges = rangeDrafts[habit.id] || cloneHabitRanges(habit);
    return (
      <div className="habit-block" key={habit.id}>
        {active && dropTarget === habit.id && <div className="habit-drop-indicator" aria-hidden="true" />}
        <div
          className={`habit-row${draggedHabitId === habit.id ? " dragging" : ""}${!active ? " inactive" : ""}`}
          data-habit-row
          data-habit-id={habit.id}
          draggable={!editing}
          onDragEnd={finishDrag}
          onDragOver={(event) => active && handleActiveRowDragOver(event, habit.id)}
          onDragStart={(event) => startDrag(event, habit)}
        >
          <span className="habit-grip" aria-hidden="true">⠿</span>
          {active && <span className="habit-order">{String(index + 1).padStart(2, "0")}</span>}
          {editing ? (
            <span className="habit-name habit-name-editing">
              <input
                aria-label={`Rename ${habit.name}`}
                autoFocus
                onBlur={() => commitRename(habit)}
                onChange={(event) => setRenameDraft(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    event.preventDefault();
                    event.currentTarget.blur();
                  }
                  if (event.key === "Escape") {
                    event.preventDefault();
                    cancelRename();
                  }
                }}
                value={renameDraft}
              />
            </span>
          ) : <span className="habit-name">{habit.name}</span>}
          <span className="habit-range-summary">{formatHabitRanges(habit)}</span>
          <span className="habit-tools">
            <button className="settings-icon-button" onClick={() => startRename(habit)} title="Rename (global typo-fix)" type="button">✎</button>
            <button className="settings-range-button" onClick={() => toggleRanges(habit)} type="button">{expanded ? "− ranges" : "+ ranges"}</button>
          </span>
        </div>
        {expanded && (
          <div className="habit-range-editor">
            <div className="habit-range-editor-head">
              <span className="section-label">validity ranges</span>
              <button className="settings-text-button" onClick={() => addRange(habit.id)} type="button">+ range</button>
            </div>
            {ranges.map((range, rangeIndex) => (
              <div className="habit-range-row" key={`${habit.id}-${rangeIndex}`}>
                <label>
                  <span>active from</span>
                  <input onChange={(event) => updateRange(habit.id, rangeIndex, "active_from", event.target.value)} type="date" value={range.active_from} />
                </label>
                <label>
                  <span>active to</span>
                  <input onChange={(event) => updateRange(habit.id, rangeIndex, "active_to", event.target.value)} type="date" value={range.active_to} />
                </label>
                <button className="settings-text-button danger" disabled={ranges.length === 1} onClick={() => removeRange(habit.id, rangeIndex)} type="button">remove</button>
              </div>
            ))}
            <div className="habit-range-footer">
              <span>Open-ended ranges stay active until paused.</span>
              <button className="settings-save-button" disabled={saving} onClick={() => saveRanges(habit)} type="button">save ranges</button>
            </div>
          </div>
        )}
      </div>
    );
  }

  function habitsPanel() {
    return (
      <>
        <div className="settings-panel-heading">
          <h2>Habits</h2>
          <span className="settings-count">{activeHabits.length} active · {habits.length} total</span>
        </div>
        <div className="settings-add-row">
          <input
            aria-label="New habit name"
            onChange={(event) => setNewHabitName(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") void addHabit();
            }}
            placeholder="New habit name"
            value={newHabitName}
          />
          <button className="settings-ghost-button" disabled={saving || !newHabitName.trim()} onClick={() => void addHabit()} type="button">+ Add</button>
        </div>
        {loading && <p className="settings-muted">loading habits…</p>}
        {!loading && habits.length === 0 && <p className="settings-muted">No habits yet. Add one above; it is active from today.</p>}
        <div className="habit-list" onDragOver={handleActiveDragOver} onDrop={(event) => void dropIntoActive(event)}>
          {activeHabits.map((habit, index) => renderHabitRow(habit, index, true))}
          {draggedHabitId != null && dropTarget === DROP_END && <div className="habit-drop-indicator" aria-hidden="true" />}
        </div>
        <div
          className={`inactive-zone${inactiveDropReady ? " drop-ready" : ""}`}
          onDragLeave={() => setInactiveDropReady(false)}
          onDragOver={(event) => {
            const habit = habits.find((item) => item.id === draggedHabitId);
            if (draggedHabitId == null || saving) return;
            setDropTarget(null);
            if (!habit || !isHabitActiveToday(habit)) return;
            event.preventDefault();
            setInactiveDropReady(true);
          }}
          onDrop={(event) => {
            setInactiveDropReady(false);
            void dropIntoInactive(event);
          }}
        >
          <span className="inactive-title">Inactive</span>
          {inactiveHabits.length === 0 && <p className="settings-muted">Drag a habit here to pause it — its validity range ends today.</p>}
          {inactiveHabits.map((habit) => renderHabitRow(habit, null, false))}
        </div>
        {draggedHabit && !isHabitActiveToday(draggedHabit) && <p className="settings-muted">Drop into the active list to resume this habit, then place it in order.</p>}
      </>
    );
  }

  function settingsValue(field) {
    return settingsInfo?.[field] || "—";
  }

  function secretValue(kind) {
    return revealedSecrets[kind] || settingsInfo?.[kind] || "—";
  }

  function storagePanel() {
    return (
      <>
        <div className="settings-panel-heading"><h2>Storage</h2></div>
        <p className="settings-sub">Path and key live in config.toml — point the path into iCloud Drive to sync. Keep a copy of the key somewhere safe: without it the database cannot be opened.</p>
        <div className="settings-kv-list">
          <div className="settings-kv">
            <span className="settings-kv-label">database path</span><span className="settings-kv-dots" />
            <code className="settings-kv-value">{settingsValue("database_path")}</code>
          </div>
          <div className="settings-kv settings-kv-secret">
            <span className="settings-kv-label">encryption key</span><span className="settings-kv-dots" />
            {editingKey ? (
              <input className="settings-secret-input" aria-label="Encryption key" onChange={(event) => setKeyDraft(event.target.value)} value={keyDraft} />
            ) : <code className="settings-kv-value">{secretValue("key")}</code>}
            {!editingKey && <button className="settings-action-button" disabled={!settingsInfo} onClick={() => void toggleSecret("key")} type="button">{revealedSecrets.key ? "hide" : "show"}</button>}
            {!editingKey && <CopyButton className="settings-action-button" disabled={!settingsInfo} onError={setError} value={() => copySecret("key")} />}
            {editingKey ? (
              <>
                <button className="settings-action-button primary" disabled={saving} onClick={() => void saveKey()} type="button">save</button>
                <button className="settings-action-button" disabled={saving} onClick={cancelKeyEdit} type="button">cancel</button>
              </>
            ) : <button className="settings-action-button" disabled={!settingsInfo || saving} onClick={() => void startKeyEdit()} type="button">edit</button>}
          </div>
        </div>
      </>
    );
  }

  function apiPanel() {
    return (
      <>
        <div className="settings-panel-heading"><h2>API</h2></div>
        <p className="settings-sub">The CLI and MCP clients authenticate with this token. Regenerating it logs out every client until they pick up the new value.</p>
        <div className="settings-kv-list">
          <div className="settings-kv">
            <span className="settings-kv-label">listening address</span><span className="settings-kv-dots" />
            <code className="settings-kv-value">{settingsValue("api_address")}</code>
          </div>
          <div className="settings-kv settings-kv-secret">
            <span className="settings-kv-label">bearer token</span><span className="settings-kv-dots" />
            <code className="settings-kv-value">{secretValue("token")}</code>
            <button className="settings-action-button" disabled={!settingsInfo} onClick={() => void toggleSecret("token")} type="button">{revealedSecrets.token ? "hide" : "show"}</button>
            <CopyButton className="settings-action-button" disabled={!settingsInfo} onError={setError} value={() => copySecret("token")} />
          </div>
        </div>
        <div className="settings-panel-footer">
          {confirmRegenerate ? (
            <div className="settings-confirm" role="alert">
              <span>Disconnect every existing client?</span>
              <button className="settings-action-button danger" disabled={saving} onClick={() => void regenerateToken()} type="button">confirm regenerate</button>
              <button className="settings-action-button" disabled={saving} onClick={() => setConfirmRegenerate(false)} type="button">cancel</button>
            </div>
          ) : <button className="settings-action-button danger" disabled={saving || !settingsInfo} onClick={() => void regenerateToken()} type="button">Regenerate token</button>}
        </div>
      </>
    );
  }

  function backupsPanel() {
    return (
      <>
        <div className="settings-panel-heading"><h2>Backups</h2></div>
        <p className="settings-sub">Daily snapshots run automatically; nothing to configure. Snapshots use the same key as the live database.</p>
        <div className="settings-kv-list">
          <div className="settings-kv">
            <span className="settings-kv-label">last backup</span><span className="settings-kv-dots" />
            <code className="settings-kv-value">{formatLocalTimestamp(settingsInfo?.last_backup)}</code>
          </div>
          <div className="settings-kv">
            <span className="settings-kv-label">folder</span><span className="settings-kv-dots" />
            <code className="settings-kv-value">{settingsValue("backups_path")}</code>
          </div>
        </div>
        {settingsInfo?.last_backup_error && <p className="settings-error">last backup error: {settingsInfo.last_backup_error}</p>}
        <div className="settings-panel-footer"><button className="settings-action-button primary" disabled={saving || settingsLoading} onClick={() => void backupNow()} type="button">Backup now</button></div>
      </>
    );
  }

  function selectedPanel() {
    if (section === "habits") return habitsPanel();
    if (section === "storage") return storagePanel();
    if (section === "api") return apiPanel();
    return backupsPanel();
  }

  return (
    <>
      <div className="rail-wrap on-top-row">
        <aside className="section-rail" aria-label="Settings sections">
          <span className="frame-title">settings</span>
          {SETTINGS_SECTIONS.map((item) => (
            <button
              className={section === item ? "on" : ""}
              type="button"
              key={item}
              onClick={(event) => selectSection(event, item)}
            >
              {item[0].toUpperCase() + item.slice(1)}
            </button>
          ))}
        </aside>
      </div>
      <main className="main-column on-top-row settings-page">
        {error && <p className="settings-error">{error.code}: {error.message}</p>}
        {settingsLoading && section !== "habits" && <p className="settings-muted">loading settings…</p>}
        {selectedPanel()}
        {settingsMessage && <p className="settings-message">{settingsMessage}</p>}
      </main>
    </>
  );
}

// Shortcuts stay out of the way of text entry; the wizard and the search box
// own their keys while focused.
function isTypingTarget(target) {
  if (!target) return false;
  if (target.isContentEditable) return true;
  const tag = target.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
}

function StatusBar({ page, summary, year, activeHabitCount, lastBackup }) {
  const average = summary?.average_rating == null ? "—" : summary.average_rating.toFixed(2);
  const monthAverage = summary?.month_average_rating == null ? "—" : summary.month_average_rating.toFixed(2);
  const habit = summary?.habit_percent == null ? "—" : `${Math.round(summary.habit_percent)}%`;
  const monthHabit = summary?.month_habit_percent == null ? "—" : `${Math.round(summary.month_habit_percent)}%`;
  // The yearly figures follow the year rail while the monthly ones are always
  // the current calendar month, so both periods are named rather than implied.
  const yearName = year == null ? "yr" : year;
  const monthName = MONTHS[new Date().getMonth()].toLowerCase();
  // The server injects the binary version into the page; it is absent in dev.
  const version = window.__DELTA_VERSION__;
  return (
    <footer className="status-bar">
      <span className="page-chip">{page.toUpperCase()}</span>
      {page === "settings" ? (
        <>
          <span>{activeHabitCount} active habits</span>
          <span>last backup {formatLocalTimestamp(lastBackup)}</span>
        </>
      ) : (
        <>
          <span>{summary?.entries ?? 0} entries</span>
          <span>{summary?.characters ?? 0} chars</span>
          <span>avg {yearName} {average} · {monthName} {monthAverage}</span>
          <span>habit {yearName} {habit} · {monthName} {monthHabit}</span>
        </>
      )}
      {version && <span className="status-version">{version}</span>}
      <span className="key-hints">
        <kbd>/</kbd> search · <kbd>n</kbd> new entry · <kbd>t</kbd> toggle view · <kbd>p</kbd> hold for phases
      </span>
    </footer>
  );
}

export default function App() {
  const [setupInfo, setSetupInfo] = useState(null);
  const [page, setPage] = useState(pageFromHash);
  const [settingsSection, setSettingsSection] = useState(settingsSectionFromHash);
  const [year, setYear] = useState(new Date().getFullYear());
  const [grid, setGrid] = useState(null);
  const [gridError, setGridError] = useState("");
  const [view, setView] = useState("rating");
  const [marker, setMarker] = useState(false);
  const [wizardDate, setWizardDate] = useState(null);
  const [popupDate, setPopupDate] = useState(null);
  const [gridRefresh, setGridRefresh] = useState(0);
  const [activeHabitCount, setActiveHabitCount] = useState(0);
  const [lastBackup, setLastBackup] = useState("");
  const searchInputRef = useRef(null);
  const pendingSearchFocus = useRef(false);
  const modalOpen = Boolean(wizardDate || popupDate);

  useEffect(() => {
    let active = true;
    apiFetch("/api/setup")
      .then(async (response) => {
        if (!response.ok) return null;
        return response.json();
      })
      .then((info) => {
        if (active && info?.mode === "setup") setSetupInfo(info);
      })
      .catch((requestError) => {
        if (active) console.warn("setup probe failed", requestError.message);
      });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    const onHashChange = () => {
      setPage(pageFromHash());
      setSettingsSection(settingsSectionFromHash());
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  // The grid is fetched here, not in GridPage, so the status bar shows the
  // same year summary while the grid page is unmounted.
  useEffect(() => {
    const controller = new AbortController();
    setGridError("");
    apiRequest(`/api/grid?year=${year}`, { signal: controller.signal }, {
      fallbackMessage: "grid request failed",
      fallbackCode: "grid_error",
    })
      .then((body) => setGrid(body))
      .catch((requestError) => {
        if (requestError.name !== "AbortError") {
          setGrid(null);
          setGridError(requestError.message);
        }
      });
    return () => controller.abort();
  }, [year, gridRefresh]);

  function navigate(nextHash) {
    window.history.replaceState(null, "", `#${nextHash}`);
    setPage(pageForHash(nextHash));
    setSettingsSection(settingsSectionForHash(nextHash));
  }

  function focusSearch() {
    const input = searchInputRef.current;
    if (!input) return;
    input.focus();
    input.select();
  }

  // Global shortcuts are bound once here because every page must answer them;
  // GridPage — which used to own the p-hold — is unmounted on stats/settings.
  useEffect(() => {
    function onKeyDown(event) {
      if (event.metaKey || event.ctrlKey || event.altKey) return;
      const key = event.key.length === 1 ? event.key.toLowerCase() : event.key;
      const input = searchInputRef.current;
      // A second slash leaves an empty search box; once a query is typed the
      // slash is ordinary text again.
      if (key === "/" && input && document.activeElement === input && input.value === "") {
        event.preventDefault();
        input.blur();
        return;
      }
      if (modalOpen || isTypingTarget(event.target)) return;
      if (key === "p") {
        setMarker(true);
        return;
      }
      if (event.repeat) return;
      if (key !== "n" && key !== "t" && key !== "/") return;
      event.preventDefault();
      if (page !== "grid") navigate("grid");
      if (key === "n") {
        setWizardDate(formatDate(new Date()));
      } else if (key === "t") {
        setView((current) => (current === "rating" ? "habit" : "rating"));
      } else if (page === "grid") {
        focusSearch();
      } else {
        // The search box mounts with the grid page; focus it once it is there.
        pendingSearchFocus.current = true;
      }
    }
    function onKeyUp(event) {
      if (event.key === "p" || event.key === "P") setMarker(false);
    }
    function onBlur() {
      setMarker(false);
    }
    window.addEventListener("keydown", onKeyDown);
    window.addEventListener("keyup", onKeyUp);
    window.addEventListener("blur", onBlur);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("keyup", onKeyUp);
      window.removeEventListener("blur", onBlur);
    };
  }, [modalOpen, page]);

  useEffect(() => {
    if (page !== "grid" || !pendingSearchFocus.current) return;
    pendingSearchFocus.current = false;
    focusSearch();
  }, [page]);

  const closePopup = useCallback(() => {
    setPopupDate(null);
  }, []);

  if (setupInfo) return <SetupWizard info={setupInfo} />;

  function openDate(date, hasEntry = false) {
    if (hasEntry) setPopupDate(date);
    else setWizardDate(date);
  }

  function closeWizard() {
    setWizardDate(null);
    setGridRefresh((value) => value + 1);
  }

  function editPopup(date) {
    setPopupDate(null);
    setWizardDate(date);
  }

  function deletePopup() {
    setPopupDate(null);
    setGridRefresh((value) => value + 1);
  }

  return (
    <div className="app-shell">
      <Header navigate={navigate} page={page} />
      <div className="page-layout">
        {page === "grid" && (
          <GridPage
            error={gridError}
            grid={grid}
            gridRefresh={gridRefresh}
            marker={marker}
            onOpen={openDate}
            searchInputRef={searchInputRef}
            setView={setView}
            view={view}
            year={year}
          />
        )}
        {page === "stats" && <StatsPage setYear={setYear} year={year} />}
        {page === "settings" && <SettingsPage navigate={navigate} onActiveHabitCountChange={setActiveHabitCount} onLastBackupChange={setLastBackup} section={settingsSection} />}
      </div>
      <StatusBar activeHabitCount={activeHabitCount} lastBackup={lastBackup} page={page} summary={grid?.summary || null} year={grid?.year ?? year} />
      {popupDate && <EntryPopup date={popupDate} onClose={closePopup} onDeleted={deletePopup} onEdit={editPopup} />}
      {wizardDate && <EntryWizard initialDate={wizardDate} onClose={closeWizard} />}
    </div>
  );
}
