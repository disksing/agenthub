export const NOTE_POOL = [
  { name: "C5", frequency: 523.25 },
  { name: "D5", frequency: 587.33 },
  { name: "E5", frequency: 659.25 },
  { name: "F5", frequency: 698.46 },
  { name: "G5", frequency: 783.99 },
  { name: "A5", frequency: 880.0 },
  { name: "B5", frequency: 987.77 },
  { name: "C6", frequency: 1046.5 },
  { name: "D6", frequency: 1174.66 },
  { name: "E6", frequency: 1318.51 },
  { name: "F6", frequency: 1396.91 },
  { name: "G6", frequency: 1567.98 },
];

export function hashSessionId(value) {
  let hash = 2166136261;
  for (const character of String(value || "")) {
    hash ^= character.codePointAt(0);
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
}

export function noteForSession(sessionId) {
  return NOTE_POOL[hashSessionId(sessionId) % NOTE_POOL.length];
}

export function quotaCycleItems(snapshot) {
  return (snapshot?.providers || []).flatMap((provider) => {
    const quota = (provider.quotas || [])[0];
    if (!quota) return [];
    return [{
      provider: provider.label || provider.provider,
      value: Math.round(Number(quota.remainingPercent) || 0),
      label: quota.kind || quota.label,
      status: quota.status || provider.status || "healthy",
      stale: Boolean(provider.stale || quota.stale),
    }];
  });
}

export function formatDuration(seconds) {
  const value = Math.max(0, Math.floor(Number(seconds) || 0));
  const days = Math.floor(value / 86400);
  const hours = Math.floor((value % 86400) / 3600);
  const minutes = Math.floor((value % 3600) / 60);
  if (days) return `${days}d ${hours}h`;
  if (hours) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}

export function activitySessions(current, frame, now = Date.now()) {
  const next = new Map(current);
  for (const session of frame?.sessions || []) {
    next.set(session.sessionId, { ...session, expiresAt: now + 5 * 60 * 1000 });
  }
  for (const [id, session] of next) {
    if (session.expiresAt <= now) next.delete(id);
  }
  return next;
}

export const ACTIVITY_VISIBLE_MS = 9000;
const ACTIVITY_LEAD_MS = 750;
const PULSE_SHAPE = [
  [420, 0], [280, 0.02], [180, 0.12], [105, 0.03], [52, 0], [24, -0.30],
  [0, 1.28], [-30, -0.42], [-70, -0.04], [-125, 0.08], [-220, 0.18],
  [-340, 0.04], [-460, 0],
];

function clamp(value, low, high) {
  return Math.min(high, Math.max(low, value));
}

function baselineMotion(sampleTimeMs, active) {
  const time = sampleTimeMs / 1000;
  const value = Math.sin(time * 2.1) * 0.018
    + Math.sin(time * 7.8 + 1.4) * 0.010
    + Math.sin(time * 18 + 0.6) * 0.005;
  return value * (active ? 1 : 0.35);
}

export function activityPulsesForFrame(frame, now = Date.now()) {
  const pulses = [];
  const sessions = [...(frame?.sessions || [])].sort((a, b) => String(a.sessionId).localeCompare(String(b.sessionId)));
  sessions.forEach((session, sessionIndex) => {
    const eventCount = Math.max(1, Math.min(8, Math.floor(Number(session.eventCount) || 1)));
    const parsedAt = Date.parse(session.lastEventAt || "");
    const baseAt = Number.isFinite(parsedAt) ? clamp(parsedAt, now - 1000, now) : now;
    const spacing = Math.min(80, 720 / eventCount);
    const baseAmplitude = 0.88 + (hashSessionId(session.sessionId) % 18) / 100;
    for (let index = 0; index < eventCount; index += 1) {
      pulses.push({
        at: baseAt - (eventCount - index - 1) * spacing - sessionIndex * 12,
        amplitude: session.completed && index === eventCount - 1 ? 1.18 : baseAmplitude,
        sessionId: session.sessionId,
      });
    }
  });
  return pulses.sort((a, b) => a.at - b.at || String(a.sessionId).localeCompare(String(b.sessionId)));
}

export function pruneActivityPulses(pulses, now = Date.now()) {
  return (pulses || []).filter((pulse) => now - pulse.at < ACTIVITY_VISIBLE_MS + ACTIVITY_LEAD_MS);
}

function pulseValue(ageOffset) {
  for (let index = 0; index < PULSE_SHAPE.length - 1; index += 1) {
    const [olderOffset, olderValue] = PULSE_SHAPE[index];
    const [newerOffset, newerValue] = PULSE_SHAPE[index + 1];
    if (ageOffset <= olderOffset && ageOffset >= newerOffset) {
      const progress = (olderOffset - ageOffset) / (olderOffset - newerOffset);
      return olderValue + (newerValue - olderValue) * progress;
    }
  }
  return 0;
}

export function waveformPoints(pulses = [], now = Date.now(), width = 700, height = 86, active = true) {
  const baseline = height * 0.54;
  const amplitude = height * 0.32;
  const visiblePulses = pruneActivityPulses(pulses, now);
  const points = [];
  for (let x = 0; x <= width; x += 2) {
    const age = ACTIVITY_VISIBLE_MS * (1 - x / width);
    const sampleTime = now - age;
    const pulseMotion = visiblePulses.reduce((sum, pulse) => (
      sum + pulseValue(age - (now - pulse.at)) * pulse.amplitude
    ), 0);
    const motion = clamp(baselineMotion(sampleTime, active) + pulseMotion, -1.15, 1.15);
    const y = baseline - motion * amplitude;
    points.push(`${x.toFixed(1)},${y.toFixed(1)}`);
  }
  return points.join(" ");
}

export function normalizeCompanionPosition(value) {
  const x = Number(value?.x);
  const y = Number(value?.y);
  return {
    x: Number.isFinite(x) ? clamp(x, 0, 1) : 1,
    y: Number.isFinite(y) ? clamp(y, 0, 1) : 1,
  };
}

export function companionPositionPixels(position, viewport, pill, gap = 12) {
  const normalized = normalizeCompanionPosition(position);
  const rangeX = Math.max(0, viewport.width - pill.width - gap * 2);
  const rangeY = Math.max(0, viewport.height - pill.height - gap * 2);
  return { x: gap + normalized.x * rangeX, y: gap + normalized.y * rangeY };
}

export function companionPositionFromPixels(pixels, viewport, pill, gap = 12) {
  const rangeX = Math.max(0, viewport.width - pill.width - gap * 2);
  const rangeY = Math.max(0, viewport.height - pill.height - gap * 2);
  return normalizeCompanionPosition({
    x: rangeX ? (clamp(pixels.x, gap, gap + rangeX) - gap) / rangeX : 0,
    y: rangeY ? (clamp(pixels.y, gap, gap + rangeY) - gap) / rangeY : 0,
  });
}

export function companionPlacement(anchor, viewport, pill, cardWidth = 380, gap = 12) {
  const vertical = anchor.y + pill.height / 2 <= viewport.height / 2 ? "down" : "up";
  const horizontal = anchor.x + pill.width / 2 <= viewport.width / 2 ? "right" : "left";
  const width = Math.min(cardWidth, Math.max(0, viewport.width - gap * 2));
  const preferredLeft = horizontal === "right" ? anchor.x : anchor.x + pill.width - width;
  const left = clamp(preferredLeft, gap, Math.max(gap, viewport.width - width - gap));
  const maxHeight = vertical === "down"
    ? Math.max(0, viewport.height - gap - anchor.y)
    : Math.max(0, anchor.y + pill.height - gap);
  return {
    vertical,
    horizontal,
    left,
    top: vertical === "down" ? anchor.y : null,
    bottom: vertical === "up" ? viewport.height - anchor.y - pill.height : null,
    maxHeight,
  };
}
