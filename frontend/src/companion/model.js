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

export function waveformPoints(pulseCount = 0) {
  const points = [];
  const baseline = 48;
  for (let x = 0; x <= 700; x += 14) {
    const pulse = (x + pulseCount * 41) % 168;
    let y = baseline + Math.sin((x + pulseCount * 9) / 34) * 2;
    if (pulse === 70) y = 16;
    else if (pulse === 84) y = 76;
    else if (pulse === 56 || pulse === 98) y = 42;
    points.push(`${x},${Math.round(y)}`);
  }
  return points.join(" ");
}
