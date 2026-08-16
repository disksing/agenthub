import { useRef } from "react";
import { Play } from "@phosphor-icons/react";
import { COMPLETION_SOUNDS, TonePlayer } from "../companion/audio.js";
import { BEEP_CHORDS, BEEP_PROGRESSIONS } from "../companion/chords.js";
import { quotaVisibilityKey } from "../companion/model.js";
import { Toggle } from "./fields.jsx";

export function ActivityPanel({ value, mutate, quota }) {
  const player = useRef(new TonePlayer());
  const update = (field, nextValue) => mutate((next) => { next[field] = nextValue; });
  const quotaEntries = (quota?.providers || []).flatMap((provider) => (
    (provider.quotas || []).map((item) => ({ provider, item, key: quotaVisibilityKey(provider, item) }))
  ));
  const hiddenQuotas = new Set(value.hiddenQuotaKeys || []);
  const toggleQuota = (key, visible) => mutate((next) => {
    const hidden = new Set(next.hiddenQuotaKeys || []);
    if (visible) hidden.delete(key);
    else hidden.add(key);
    next.hiddenQuotaKeys = [...hidden].sort();
  });
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
      <section className="settings-card">
        <div className="settings-card-heading"><div><h3>Quota visibility</h3><p>Choose which current quota rows appear in Beeper and its collapsed rotation.</p></div></div>
        {quotaEntries.map(({ provider, item, key }) => {
          const providerLabel = provider.label || provider.provider;
          const quotaLabel = item.label || item.kind;
          const visible = !hiddenQuotas.has(key);
          return (
            <div className="settings-toggle-row" key={key}>
              <div>
                <strong>{providerLabel} / {quotaLabel}</strong>
                <small>{Math.round(Number(item.remainingPercent) || 0)}% remaining</small>
              </div>
              <Toggle checked={visible} label={`Show ${providerLabel} / ${quotaLabel}`} onChange={(checked) => toggleQuota(key, checked)} />
            </div>
          );
        })}
        {!quotaEntries.length ? <div className="settings-empty">{quota?.error || "No current quota data."}</div> : null}
      </section>
    </div>
  );
}
