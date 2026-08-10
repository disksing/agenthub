import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Gear, Play, SpeakerHigh, SpeakerSlash, X } from "@phosphor-icons/react";
import { api } from "../api.js";
import { COMPLETION_SOUNDS, normalizeCompletionSound, TonePlayer } from "./audio.js";
import { ActivityWaveform } from "./ActivityWaveform.jsx";
import {
  activityPulsesForFrame, activitySessions, companionPlacement, companionPositionFromPixels,
  companionPositionPixels, formatDuration, normalizeCompanionPosition, noteForSession,
  pruneActivityPulses, quotaCycleItems,
} from "./model.js";

const DEFAULT_COMPANION = {
  showActivity: true,
  enableBeeping: true,
  beepVolume: 0.28,
  completionSound: "completed-voice",
};
const POSITION_STORAGE_KEY = "agenthub.companion.position.v1";
const DEFAULT_PILL_SIZE = { width: 236, height: 42 };

function viewportSize() {
  return { width: window.innerWidth, height: window.innerHeight };
}

function storedPosition() {
  try {
    return normalizeCompanionPosition(JSON.parse(window.localStorage.getItem(POSITION_STORAGE_KEY) || "null"));
  } catch {
    return { x: 1, y: 1 };
  }
}

function statusTone(status) {
  if (status === "critical" || status === "danger") return "danger";
  if (status === "warning") return "warning";
  return "healthy";
}

function updatedAgo(value) {
  const time = Date.parse(value || "");
  if (!Number.isFinite(time)) return "not updated";
  const seconds = Math.max(0, Math.floor((Date.now() - time) / 1000));
  if (seconds < 60) return "just now";
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  return `${Math.floor(seconds / 3600)}h ago`;
}

function QuotaRow({ quota }) {
  const tone = statusTone(quota.status);
  const left = Math.round(Number(quota.remainingPercent) || 0);
  return (
    <div className={`companion-quota-row ${tone}`}>
      <div className="companion-quota-top">
        <span>{quota.label || quota.kind}</span>
        <strong>{left}% <small>left</small></strong>
      </div>
      <div className="companion-quota-track">
        <span className="companion-quota-fill" style={{ width: `${Math.max(0, Math.min(100, left))}%` }} />
        {quota.windowPositionPercent != null ? (
          <span className="companion-quota-cursor" style={{ left: `${quota.windowPositionPercent}%` }} />
        ) : null}
      </div>
      <div className="companion-quota-under">
        <span>{quota.resetInSeconds != null ? `resets in ${formatDuration(quota.resetInSeconds)}` : `${Math.round(quota.usedPercent || 0)}% used`}</span>
        <span>{quota.stale ? "stale" : quota.windowPositionPercent != null ? `${Math.round(quota.windowPositionPercent)}% to reset` : quota.status}</span>
      </div>
    </div>
  );
}

export function Companion({ revision = 0, onOpenSettings }) {
  const [open, setOpen] = useState(false);
  const [settings, setSettings] = useState({ companion: DEFAULT_COMPANION, onWatch: {} });
  const [quota, setQuota] = useState({ configured: false, connected: false, providers: [] });
  const [quotaLoading, setQuotaLoading] = useState(true);
  const [quotaIndex, setQuotaIndex] = useState(0);
  const [cyclePaused, setCyclePaused] = useState(false);
  const [activityState, setActivityState] = useState("connecting");
  const [activeSessions, setActiveSessions] = useState(() => new Map());
  const [activityPulses, setActivityPulses] = useState([]);
  const [audioBlocked, setAudioBlocked] = useState(false);
  const [savingControl, setSavingControl] = useState(false);
  const [controlError, setControlError] = useState("");
  const [position, setPosition] = useState(storedPosition);
  const [viewport, setViewport] = useState(viewportSize);
  const [pillSize, setPillSize] = useState(DEFAULT_PILL_SIZE);
  const [dragging, setDragging] = useState(false);
  const tonePlayer = useRef(new TonePlayer());
  const sequence = useRef(0);
  const pillRef = useRef(null);
  const dragState = useRef(null);
  const suppressClick = useRef(false);

  const companion = {
    ...DEFAULT_COMPANION,
    ...(settings.companion || {}),
    completionSound: normalizeCompletionSound(settings.companion?.completionSound),
  };
  const cycleItems = useMemo(() => quotaCycleItems(quota), [quota]);
  const cycleItem = cycleItems[quotaIndex % Math.max(1, cycleItems.length)];
  const activeList = useMemo(() => [...activeSessions.values()].sort((a, b) => a.sessionId.localeCompare(b.sessionId)), [activeSessions]);
  const anchor = companionPositionPixels(position, viewport, pillSize);
  const placement = companionPlacement(anchor, viewport, pillSize);
  const layerStyle = open ? {
    left: placement.left,
    top: placement.top == null ? "auto" : placement.top,
    bottom: placement.bottom == null ? "auto" : placement.bottom,
    "--companion-max-height": `${placement.maxHeight}px`,
  } : { left: anchor.x, top: anchor.y, bottom: "auto" };

  const loadQuota = async () => {
    setQuotaLoading(true);
    try {
      const body = await api("/v1/quota");
      setQuota(body.quota || { configured: false, connected: false, providers: [] });
    } catch (error) {
      setQuota((current) => ({ ...current, connected: false, stale: true, error: error.message }));
    } finally {
      setQuotaLoading(false);
    }
  };

  useEffect(() => {
    let disposed = false;
    api("/v1/config").then((body) => {
      if (!disposed) setSettings(body.config || { companion: DEFAULT_COMPANION, onWatch: {} });
    }).catch(() => {});
    return () => { disposed = true; };
  }, [revision]);

  useEffect(() => {
    let disposed = false;
    let timer;
    const refresh = async () => {
      if (!disposed) await loadQuota();
    };
    refresh();
    const seconds = Number(settings.onWatch?.refreshIntervalSeconds) || 60;
    timer = window.setInterval(refresh, Math.max(30, seconds) * 1000);
    return () => { disposed = true; window.clearInterval(timer); };
  }, [revision, settings.onWatch?.enabled, settings.onWatch?.refreshIntervalSeconds]);

  useEffect(() => {
    if (cyclePaused || cycleItems.length < 2) return undefined;
    const timer = window.setInterval(() => setQuotaIndex((current) => (current + 1) % cycleItems.length), 3000);
    return () => window.clearInterval(timer);
  }, [cycleItems.length, cyclePaused]);

  useEffect(() => {
    setQuotaIndex((current) => current % Math.max(1, cycleItems.length));
  }, [cycleItems.length]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      const now = Date.now();
      setActiveSessions((current) => activitySessions(current, { sessions: [] }, now));
      setActivityPulses((current) => pruneActivityPulses(current, now));
    }, 1000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    const onResize = () => setViewport(viewportSize());
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  useLayoutEffect(() => {
    if (open || !pillRef.current) return;
    const rect = pillRef.current.getBoundingClientRect();
    if (rect.width && rect.height) setPillSize({ width: rect.width, height: rect.height });
  }, [open, cycleItem, companion.enableBeeping]);

  useEffect(() => {
    try { window.localStorage.setItem(POSITION_STORAGE_KEY, JSON.stringify(position)); } catch { /* Position persistence is best-effort. */ }
  }, [position]);

  useEffect(() => {
    if (!companion.enableBeeping) return undefined;
    const unlock = () => {
      tonePlayer.current.resume()
        .then((running) => setAudioBlocked(!running))
        .catch(() => setAudioBlocked(true));
    };
    window.addEventListener("pointerdown", unlock, { once: true });
    window.addEventListener("keydown", unlock, { once: true });
    return () => {
      window.removeEventListener("pointerdown", unlock);
      window.removeEventListener("keydown", unlock);
    };
  }, [companion.enableBeeping]);

  useEffect(() => {
    if (!companion.showActivity) {
      setActivityState("paused");
      return undefined;
    }
    let disposed = false;
    const source = new EventSource("/v1/activity/events");
    setActivityState("connecting");
    source.onopen = () => { if (!disposed) setActivityState("live"); };
    source.onerror = () => { if (!disposed) setActivityState("connecting"); };
    source.onmessage = (message) => {
      if (disposed) return;
      const frame = JSON.parse(message.data);
      if (sequence.current && frame.sequence !== sequence.current + 1) {
        setActiveSessions(new Map());
        setActivityPulses([]);
      }
      sequence.current = frame.sequence;
      setActiveSessions((current) => activitySessions(current, frame));
      const receivedAt = Date.now();
      setActivityPulses((current) => pruneActivityPulses([
        ...current,
        ...activityPulsesForFrame(frame, receivedAt),
      ], receivedAt));
      if (!companion.enableBeeping) return;
      const sessions = frame.sessions || [];
      const spacing = sessions.length > 1 ? Math.min(0.8 / sessions.length, 0.08) : 0;
      sessions.forEach((session, index) => {
        if (session.completed) tonePlayer.current.completion(companion.completionSound, companion.beepVolume);
        else tonePlayer.current.pulse(session.sessionId, companion.beepVolume, index * spacing);
      });
      setAudioBlocked(tonePlayer.current.status() !== "running");
    };
    return () => { disposed = true; source.close(); };
  }, [companion.showActivity, companion.enableBeeping, companion.beepVolume, companion.completionSound]);

  const resumeAudio = async () => {
    if (!companion.enableBeeping) return;
    const running = await tonePlayer.current.resume();
    setAudioBlocked(!running);
  };

  const openCard = async () => {
    setOpen(true);
    await resumeAudio();
  };

  const startDrag = (event) => {
    if (event.button !== 0) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    dragState.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      originX: anchor.x,
      originY: anchor.y,
      moved: false,
    };
    setCyclePaused(true);
  };

  const moveDrag = (event) => {
    const current = dragState.current;
    if (!current || current.pointerId !== event.pointerId) return;
    const dx = event.clientX - current.startX;
    const dy = event.clientY - current.startY;
    if (Math.hypot(dx, dy) >= 4) {
      current.moved = true;
      setDragging(true);
    }
    if (!current.moved) return;
    setPosition(companionPositionFromPixels(
      { x: current.originX + dx, y: current.originY + dy },
      viewport,
      pillSize,
    ));
  };

  const finishDrag = (event) => {
    const current = dragState.current;
    if (!current || current.pointerId !== event.pointerId) return;
    suppressClick.current = current.moved && event.type === "pointerup";
    dragState.current = null;
    setDragging(false);
    setCyclePaused(false);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
  };

  const clickPill = (event) => {
    if (suppressClick.current) {
      suppressClick.current = false;
      event.preventDefault();
      return;
    }
    openCard();
  };

  const saveCompanion = async (patch) => {
    if (savingControl) return;
    setSavingControl(true);
    setControlError("");
    try {
      const current = await api("/v1/config");
      const next = {
        ...current.config,
        companion: { ...DEFAULT_COMPANION, ...current.config?.companion, ...patch },
      };
      const body = await api("/v1/config", { method: "PUT", body: JSON.stringify({ config: next }) });
      setSettings(body.config || next);
    } catch (value) {
      setControlError(value.message || "Failed to save companion settings");
    } finally {
      setSavingControl(false);
    }
  };

  const toggleBeeping = async () => {
    const enabled = !companion.enableBeeping;
    if (enabled) await tonePlayer.current.resume();
    await saveCompanion({ enableBeeping: enabled });
  };

  const preview = async () => {
    if (await tonePlayer.current.resume()) {
      tonePlayer.current.completion(companion.completionSound, companion.beepVolume);
      setAudioBlocked(false);
    }
  };

  const connectionLabel = !quota.configured ? "Not configured" : quota.connected ? "Connected" : "Disconnected";
  const activityLabel = activityState === "paused" ? "Paused" : activityState === "live" ? "AgentHub Live" : "Connecting";

  return (
    <div
      className={`companion-layer ${open ? "open" : "closed"}`}
      style={layerStyle}
      data-expand-vertical={open ? placement.vertical : undefined}
      data-expand-horizontal={open ? placement.horizontal : undefined}
    >
      {!open ? (
        <button
          ref={pillRef}
          type="button"
          className={`companion-pill ${dragging ? "dragging" : ""}`}
          title="Drag to move; click to open"
          aria-label={cycleItem ? `Open companion; showing ${cycleItem.provider} quota ${cycleItem.value}%` : "Open companion; no quota data"}
          onClick={clickPill}
          onPointerDown={startDrag}
          onPointerMove={moveDrag}
          onPointerUp={finishDrag}
          onPointerCancel={finishDrag}
          onMouseEnter={() => setCyclePaused(true)}
          onMouseLeave={() => setCyclePaused(false)}
          onFocus={() => setCyclePaused(true)}
          onBlur={() => setCyclePaused(false)}
        >
          <svg className="companion-spark" viewBox="0 0 52 22" preserveAspectRatio="none" aria-hidden="true">
            <polyline points="0,13 6,13 9,10 12,13 19,13 22,4 25,19 28,13 36,13 39,10 42,13 47,13 49,7 51,13" />
          </svg>
          <span className={`companion-live-dot ${activeList.length ? "active" : ""}`} />
          <span className={`companion-cycle ${cycleItem ? statusTone(cycleItem.status) : ""}`}>
            {cycleItem ? <>{cycleItem.provider} {cycleItem.value}% <small>{cycleItem.label}</small></> : "No quota data"}
          </span>
          {companion.enableBeeping ? <SpeakerHigh size={15} /> : <SpeakerSlash size={15} className="muted" />}
        </button>
      ) : (
        <section className="companion-card" aria-label="Activity and provider quota companion">
          <header className="companion-card-header">
            <span className={`companion-connection ${quota.connected ? "connected" : ""}`}><i />OnWatch · {connectionLabel}</span>
            <span className="companion-updated">{quotaLoading ? "updating…" : updatedAgo(quota.updatedAt)}</span>
            <span className="companion-header-actions">
              <button type="button" aria-label="Open companion settings" onClick={onOpenSettings}><Gear size={15} /></button>
              <button type="button" aria-label="Collapse companion" onClick={() => setOpen(false)}><X size={15} /></button>
            </span>
          </header>
          <div className="companion-scroll">
            <div className="companion-dark">
              <div className="companion-cap-row">
                <span className="companion-cap">Activity Monitor</span>
                <span className={`companion-live-state ${activityState}`}>{activityLabel}</span>
              </div>
              <div className="companion-thread-stat"><strong>{activeList.length}</strong><span>active threads · last 5 min</span></div>
              <ActivityWaveform pulses={activityPulses} live={activityState === "live"} />
              <div className="companion-thread-chips">
                {activeList.map((session) => <span key={session.sessionId}>{session.title || session.sessionId.slice(0, 8)} {noteForSession(session.sessionId).name}</span>)}
                {!activeList.length ? <span className="idle">Waiting for activity</span> : null}
              </div>
              <div className="companion-control-row">
                <div><strong>Enable beeping</strong><small>{audioBlocked ? "Click to enable audio" : "Beep while agents are active"}</small></div>
                <button type="button" role="switch" aria-checked={companion.enableBeeping} className={`companion-switch ${companion.enableBeeping ? "on" : ""}`} disabled={savingControl} onClick={toggleBeeping}><span /></button>
              </div>
              {controlError ? <p className="companion-control-error" role="alert">{controlError}</p> : null}
              <div className="companion-control-row">
                <div><strong>On finish</strong></div>
                <div className="companion-sound-controls">
                  <select value={companion.completionSound} disabled={savingControl} aria-label="Completion sound" onChange={(event) => saveCompanion({ completionSound: event.target.value })}>
                    {COMPLETION_SOUNDS.map((option) => <option value={option.value} key={option.value}>{option.label}</option>)}
                  </select>
                  <button type="button" aria-label="Preview completion sound" onClick={preview}><Play size={13} weight="fill" /></button>
                </div>
              </div>

              <div className="companion-quota-heading"><span className="companion-cap">Provider Quota</span><small>All data from OnWatch</small></div>
              {quota.error ? <div className="companion-quota-error" role="status">{quota.error}<button type="button" onClick={loadQuota}>Retry</button></div> : null}
              {(quota.providers || []).map((provider) => (
                <section className="companion-provider" key={provider.provider}>
                  <header><strong>{provider.label}</strong>{provider.planLabel ? <span>{provider.planLabel}</span> : null}<em className={statusTone(provider.status)}>{provider.stale ? "Stale" : provider.status}</em></header>
                  {provider.error ? <p className="companion-provider-error">{provider.error}</p> : null}
                  {(provider.quotas || []).map((item) => <QuotaRow quota={item} key={`${provider.provider}-${item.kind}-${item.label}`} />)}
                </section>
              ))}
              {!quotaLoading && !(quota.providers || []).length ? <p className="companion-empty-quota">No quota data</p> : null}
              <p className="companion-source-note">The marker moves left as each reset approaches.</p>
            </div>
          </div>
        </section>
      )}
    </div>
  );
}
