import { useEffect, useMemo, useState } from "react";
import { waveformPoints } from "./model.js";

export function ActivityWaveform({ pulses, live }) {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    let frame;
    const tick = () => {
      setNow(Date.now());
      frame = window.requestAnimationFrame(tick);
    };
    frame = window.requestAnimationFrame(tick);
    return () => window.cancelAnimationFrame(frame);
  }, []);

  const points = useMemo(() => waveformPoints(pulses, now, 700, 86, live), [pulses, now, live]);
  return (
    <div className="companion-ecg" data-real-pulse-count={pulses.length}>
      <svg viewBox="0 0 700 86" preserveAspectRatio="none" aria-label="Live activity waveform">
        <g className="companion-ecg-grid"><line x1="0" y1="28" x2="700" y2="28" /><line x1="0" y1="57" x2="700" y2="57" /></g>
        <polyline className="companion-ecg-line" points={points} />
      </svg>
      {live ? <span className="companion-scanbar" /> : null}
    </div>
  );
}
