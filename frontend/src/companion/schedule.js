export function pulsePlaybackOffsets(itemCount, windowSeconds = 1) {
  const count = Math.max(0, Math.floor(Number(itemCount) || 0));
  if (!count) return [];
  if (count === 1) return [0];
  const window = Math.max(0, Number(windowSeconds) || 0);
  return Array.from({ length: count }, (_, index) => index * window / count);
}
