import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { CheckCircle, Warning, X } from "@phosphor-icons/react";
import { api } from "../api";
import { buildPayload, createDraft, isDirty, normalizeConfig, validateDraft } from "./configModel";
import { ProvidersPanel } from "./ProvidersPanel";
import { AgentsPanel } from "./AgentsPanel";
import { ProfilesPanel } from "./ProfilesPanel";
import { GeneralPanel } from "./GeneralPanel";

const SECTIONS = [
  { id: "providers", label: "提供方" },
  { id: "agents", label: "Agent" },
  { id: "profiles", label: "Profile 路由" },
  { id: "general", label: "常规" },
];

// draft 只包含 JSON 安全数据（全部来自 normalizeConfig），用 JSON 深拷贝即可。
function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

export function SettingsModal({ onClose, onSaved, triggerRef }) {
  const [phase, setPhase] = useState("loading");
  const [loadError, setLoadError] = useState("");
  const [draft, setDraft] = useState(null);
  const [snapshot, setSnapshot] = useState(null);
  const [probes, setProbes] = useState([]);
  const [section, setSection] = useState("providers");
  const [showErrors, setShowErrors] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const [conflict, setConflict] = useState(false);
  const [savedOk, setSavedOk] = useState(false);
  const dialogRef = useRef(null);
  const savedTimer = useRef(null);

  const dirty = draft && snapshot ? isDirty(draft, snapshot) : false;
  const errors = useMemo(() => (draft ? validateDraft(draft) : []), [draft]);

  const load = useCallback(async () => {
    setPhase("loading");
    setLoadError("");
    setConflict(false);
    setSaveError("");
    setShowErrors(false);
    try {
      const [configBody, agentsBody] = await Promise.all([api("/v1/config"), api("/v1/agents")]);
      const next = createDraft(configBody.config || {});
      setDraft(next);
      setSnapshot(clone(next));
      setProbes(agentsBody.probes || []);
      setPhase("ready");
    } catch (value) {
      setLoadError(value.message || "加载配置失败");
      setPhase("error");
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // 初始焦点进入对话框；卸载时焦点回到触发按钮。
  useEffect(() => {
    dialogRef.current?.focus();
    return () => {
      clearTimeout(savedTimer.current);
      triggerRef?.current?.focus();
    };
  }, [triggerRef]);

  const requestClose = useCallback(() => {
    if (dirty && !window.confirm("设置尚未保存，确定要关闭并丢弃修改吗？")) return;
    onClose();
  }, [dirty, onClose]);

  useEffect(() => {
    const onKeyDown = (event) => {
      if (event.key === "Escape") requestClose();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [requestClose]);

  const mutate = useCallback((recipe) => {
    setSaveError("");
    setSavedOk(false);
    setDraft((current) => {
      const next = clone(current);
      recipe(next);
      return next;
    });
  }, []);

  const replace = useCallback((nextDraft) => {
    setSaveError("");
    setSavedOk(false);
    setDraft(nextDraft);
  }, []);

  const save = async (force = false) => {
    if (saving || !draft) return;
    if (errors.length) {
      setShowErrors(true);
      setSection(errors[0].section);
      return;
    }
    setSaving(true);
    setSaveError("");
    try {
      if (!force) {
        // 并发保护：保存前确认服务端配置未被其他客户端修改。
        const current = await api("/v1/config");
        if (JSON.stringify(normalizeConfig(current.config)) !== JSON.stringify(normalizeConfig(snapshot))) {
          setConflict(true);
          return;
        }
      }
      const payload = buildPayload(draft);
      await api("/v1/config", { method: "PUT", body: JSON.stringify({ config: payload }) });
      const agentsBody = await api("/v1/agents");
      setProbes(agentsBody.probes || []);
      setSnapshot(clone(payload));
      setShowErrors(false);
      setConflict(false);
      setSavedOk(true);
      clearTimeout(savedTimer.current);
      savedTimer.current = setTimeout(() => setSavedOk(false), 3000);
      onSaved?.();
    } catch (value) {
      setSaveError(value.message || "保存失败");
    } finally {
      setSaving(false);
    }
  };

  const sectionErrorCount = (id) => errors.filter((item) => item.section === id).length;

  return (
    <div
      className="modal-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) requestClose();
      }}
    >
      <section
        className="settings-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="settings-dialog-title"
        ref={dialogRef}
        tabIndex={-1}
      >
        <header className="settings-dialog-header">
          <div>
            <h2 id="settings-dialog-title">设置</h2>
            <p>配置提供方、Agent、Profile 路由与默认行为，保存后统一生效。</p>
          </div>
          <button className="icon-button" aria-label="关闭设置" onClick={requestClose}>
            <X size={19} />
          </button>
        </header>

        {phase === "loading" ? <div className="settings-status">正在加载配置…</div> : null}
        {phase === "error" ? (
          <div className="settings-status">
            <p className="settings-load-error" role="alert">{loadError}</p>
            <button className="settings-button" onClick={load}>重试</button>
          </div>
        ) : null}

        {phase === "ready" ? (
          <div className="settings-body">
            <nav className="settings-nav" aria-label="设置分类">
              {SECTIONS.map((item) => {
                const count = sectionErrorCount(item.id);
                return (
                  <button
                    key={item.id}
                    className={`settings-nav-button ${section === item.id ? "active" : ""}`}
                    aria-current={section === item.id ? "true" : undefined}
                    onClick={() => setSection(item.id)}
                  >
                    <span>{item.label}</span>
                    {showErrors && count ? <span className="settings-nav-badge">{count}</span> : null}
                  </button>
                );
              })}
            </nav>
            <div className="settings-content">
              <div className="settings-panel">
                {section === "providers" ? (
                  <ProvidersPanel draft={draft} probes={probes} errors={errors} showErrors={showErrors} mutate={mutate} replace={replace} />
                ) : null}
                {section === "agents" ? (
                  <AgentsPanel draft={draft} probes={probes} errors={errors} showErrors={showErrors} mutate={mutate} replace={replace} />
                ) : null}
                {section === "profiles" ? (
                  <ProfilesPanel draft={draft} probes={probes} errors={errors} showErrors={showErrors} mutate={mutate} replace={replace} />
                ) : null}
                {section === "general" ? (
                  <GeneralPanel draft={draft} probes={probes} errors={errors} showErrors={showErrors} mutate={mutate} replace={replace} />
                ) : null}
              </div>
              <footer className="settings-savebar">
                {conflict ? (
                  <div className="settings-conflict" role="alert">
                    <span><Warning size={15} />配置已被其他客户端修改，直接保存会覆盖对方的改动。</span>
                    <button onClick={load} disabled={saving}>重新加载</button>
                    <button onClick={() => save(true)} disabled={saving}>强制覆盖</button>
                  </div>
                ) : null}
                {saveError ? <p className="settings-save-error" role="alert">{saveError}</p> : null}
                <div className="settings-savebar-row">
                  <div className="settings-savebar-status">
                    {dirty ? <span className="settings-dirty">有未保存的修改</span> : null}
                    {savedOk && !dirty ? (
                      <span className="settings-save-ok" role="status"><CheckCircle size={15} />已保存</span>
                    ) : null}
                  </div>
                  <div className="settings-savebar-actions">
                    <button className="settings-button" onClick={requestClose} disabled={saving}>取消</button>
                    <button
                      className="settings-button settings-button-primary"
                      onClick={() => save(false)}
                      disabled={!dirty || saving}
                    >
                      {saving ? "保存中…" : "保存全部"}
                    </button>
                  </div>
                </div>
              </footer>
            </div>
          </div>
        ) : null}
      </section>
    </div>
  );
}
