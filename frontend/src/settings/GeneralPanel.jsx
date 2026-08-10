import { useState } from "react";
import { CheckCircle, CircleNotch, Warning } from "@phosphor-icons/react";
import { api } from "../api.js";
import { FieldError, Toggle } from "./fields.jsx";

export function GeneralPanel({ draft, errors, showErrors, mutate }) {
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState(null);
  const onWatch = draft.onWatch;
  const error = (field) => showErrors ? errors.find((item) => item.section === "general" && item.field === field) : null;
  const update = (field, value) => mutate((next) => { next.onWatch[field] = value; });

  const testConnection = async () => {
    if (testing) return;
    setTesting(true);
    setTestResult(null);
    try {
      const result = await api("/v1/onwatch/test", { method: "POST", body: JSON.stringify({ onWatch }) });
      setTestResult({ ok: true, message: `Connected · ${result.providers?.length || 0} providers` });
    } catch (value) {
      setTestResult({ ok: false, message: value.message || "Connection failed" });
    } finally {
      setTesting(false);
    }
  };

  return (
    <div className="settings-section-stack">
      <section className="settings-card">
        <div className="settings-card-heading"><div><h3>OnWatch Integration</h3><p>Connect the companion to the local quota service.</p></div><Toggle checked={onWatch.enabled} label="Enable OnWatch" onChange={(value) => update("enabled", value)} /></div>
        <label className="settings-field">
          <span>Server URL</span>
          <input className={error("serverUrl") ? "invalid" : ""} value={onWatch.serverUrl} onChange={(event) => update("serverUrl", event.target.value)} placeholder="http://127.0.0.1:9211" />
          <small>Base URL of the OnWatch quota service. Credentials in URLs are rejected.</small>
          <FieldError error={error("serverUrl")} />
        </label>
        <label className="settings-field">
          <span>Authentication</span>
          <select value={onWatch.authMode} onChange={(event) => update("authMode", event.target.value)}>
            <option value="trusted_proxy">Trusted proxy</option><option value="basic">Basic Auth</option><option value="none">None</option>
          </select>
        </label>
        {onWatch.authMode !== "none" ? (
          <label className="settings-field">
            <span>{onWatch.authMode === "trusted_proxy" ? "Forwarded user" : "Username"}</span>
            <input className={error("username") ? "invalid" : ""} value={onWatch.username} onChange={(event) => update("username", event.target.value)} placeholder="admin" autoComplete="username" />
            <FieldError error={error("username")} />
          </label>
        ) : null}
        {onWatch.authMode === "basic" ? (
          <label className="settings-field">
            <span>Password</span>
            <input type="password" value={onWatch.password} onChange={(event) => update("password", event.target.value)} placeholder="Leave blank to keep the stored password" autoComplete="new-password" />
            <small>The daemon never returns the stored password to the browser.</small>
          </label>
        ) : null}
        <label className="settings-field">
          <span>Refresh interval</span>
          <select value={onWatch.refreshIntervalSeconds} onChange={(event) => update("refreshIntervalSeconds", Number(event.target.value))}>
            <option value={30}>Every 30 seconds</option><option value={60}>Every 60 seconds</option><option value={300}>Every 5 minutes</option>
          </select>
        </label>
        <div className="settings-test-row">
          <button type="button" className="settings-button" disabled={testing} onClick={testConnection}>{testing ? <><CircleNotch className="spin" size={14} />Testing…</> : "Test Connection"}</button>
          {testResult ? <span className={testResult.ok ? "ok" : "error"}>{testResult.ok ? <CheckCircle size={15} /> : <Warning size={15} />}{testResult.message}</span> : null}
        </div>
      </section>
    </div>
  );
}
