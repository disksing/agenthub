import { useRef } from "react";
import { Play } from "@phosphor-icons/react";
import { COMPLETION_SOUNDS, TonePlayer } from "../companion/audio.js";
import { BEEP_CHORDS, BEEP_PROGRESSIONS } from "../companion/chords.js";
import { Toggle } from "./fields.jsx";

export function ActivityPanel({ value, mutate }) {
  const player = useRef(new TonePlayer());
  const update = (field, nextValue) => mutate((next) => { next[field] = nextValue; });
  const previewCompletion = async () => {
    if (await player.current.resume()) player.current.completion(value.completionSound, value.beepVolume);
  };
  const previewChord = async () => {
    if (await player.current.resume()) player.current.previewProgression(value.beepProgression, value.beepChord, value.beepVolume);
  };
  return (
    <div className="settings-section-stack">
      <section className="settings-card">
        <div className="settings-card-heading"><div><h3>Activity Monitor</h3><p>Control the global activity stream, beeps, and completion sounds.</p></div></div>
        <div className="settings-toggle-row"><div><strong>Show activity</strong><small>Subscribe to AgentHub's live Session activity stream.</small></div><Toggle checked={value.showActivity} label="Show activity" onChange={(checked) => update("showActivity", checked)} /></div>
        <div className="settings-toggle-row"><div><strong>Enable beeping</strong><small>Play at most one pulse per active Session each second.</small></div><Toggle checked={value.enableBeeping} label="Enable beeping" onChange={(checked) => update("enableBeeping", checked)} /></div>
        <label className="settings-field">
          <span>Chord movement</span>
          <select value={value.beepProgression} onChange={(event) => update("beepProgression", event.target.value)}>
            {BEEP_PROGRESSIONS.map((option) => <option value={option.value} key={option.value}>{option.label}</option>)}
          </select>
          <small>{BEEP_PROGRESSIONS.find((option) => option.value === value.beepProgression)?.description}</small>
        </label>
        <label className="settings-field">
          <span>{value.beepProgression === "single" ? "Beep chord" : "Single-chord base"}</span>
          <div className="settings-preview-row">
            <select value={value.beepChord} disabled={value.beepProgression !== "single"} onChange={(event) => update("beepChord", event.target.value)}>
              <optgroup label="Major">
                {BEEP_CHORDS.filter((option) => option.quality === "major").map((option) => <option value={option.value} key={option.value}>{option.label} · {option.noteNames.join(" ")}</option>)}
              </optgroup>
              <optgroup label="Minor">
                {BEEP_CHORDS.filter((option) => option.quality === "minor").map((option) => <option value={option.value} key={option.value}>{option.label} · {option.noteNames.join(" ")}</option>)}
              </optgroup>
            </select>
            <button type="button" className="settings-button" onClick={previewChord}><Play size={13} weight="fill" />Preview</button>
          </div>
        </label>
        <label className="settings-field">
          <span>Beep volume <output>{Math.round(value.beepVolume * 100)}%</output></span>
          <input type="range" min="0" max="1" step="0.01" value={value.beepVolume} onChange={(event) => update("beepVolume", Number(event.target.value))} />
        </label>
        <label className="settings-field">
          <span>Completion sound</span>
          <div className="settings-preview-row">
            <select value={value.completionSound} onChange={(event) => update("completionSound", event.target.value)}>
              {COMPLETION_SOUNDS.map((option) => <option value={option.value} key={option.value}>{option.label}</option>)}
            </select>
            <button type="button" className="settings-button" onClick={previewCompletion}><Play size={13} weight="fill" />Preview</button>
          </div>
        </label>
      </section>
    </div>
  );
}
